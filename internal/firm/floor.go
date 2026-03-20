package firm

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hnic/trading-floor/internal/graphdb"
	"github.com/hnic/trading-floor/internal/observe"
	"github.com/hnic/trading-floor/internal/store"
	"github.com/hnic/trading-floor/internal/trace"
	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/pkg/signal"
)

type deskTask struct {
	desk *Desk
	sig  signal.Signal
}

// Floor is the main orchestrator — runs 24/7, fans signals to desks
type Floor struct {
	log       *slog.Logger
	wire      *wire.Manager
	desks     []*Desk
	sessionID string
	store     *store.DB
	graph     *graphdb.Client
	mu        sync.RWMutex
	once      sync.Once

	taskQueue chan deskTask

	workerCount      int
	enqueueTimeout   time.Duration
	slowTaskWarnAt   time.Duration
	signalsProcessed atomic.Int64
	tradesExecuted   atomic.Int64
	tasksEnqueued    atomic.Int64
	tasksStarted     atomic.Int64
	tasksCompleted   atomic.Int64
	tasksDropped     atomic.Int64
	tasksSkipped     atomic.Int64
	activeTasks      atomic.Int64
}

func NewFloor(wireMgr *wire.Manager, sessionID string) *Floor {
	workerCount := readEnvInt("FLOOR_WORKERS", 4)
	queueSize := readEnvInt("FLOOR_TASK_QUEUE_SIZE", 2048)
	enqueueTimeout := readEnvDuration("FLOOR_TASK_ENQUEUE_TIMEOUT", 250*time.Millisecond)
	slowTaskWarnAt := readEnvDuration("FLOOR_SLOW_TASK_WARN_AT", 45*time.Second)

	return &Floor{
		log:            slog.Default().With("component", "floor", "session_id", sessionID),
		wire:           wireMgr,
		sessionID:      sessionID,
		taskQueue:      make(chan deskTask, queueSize),
		workerCount:    workerCount,
		enqueueTimeout: enqueueTimeout,
		slowTaskWarnAt: slowTaskWarnAt,
	}
}

// AddDesk adds a desk to the floor
func (f *Floor) AddDesk(desk *Desk) {
	f.mu.Lock()
	f.desks = append(f.desks, desk)
	f.mu.Unlock()
	f.log.Info("desk added",
		"id", desk.ID,
		"domain", desk.Domain,
		"ab_group", desk.ABGroup,
		"capital", desk.Capital,
	)
}

// SetStore configures a floor-level persistence sink for ingress signals.
func (f *Floor) SetStore(db *store.DB) {
	f.mu.Lock()
	f.store = db
	f.mu.Unlock()
}

func (f *Floor) SetGraph(graph *graphdb.Client) {
	f.mu.Lock()
	f.graph = graph
	f.mu.Unlock()
}

// Run starts the floor — processes signals forever
func (f *Floor) Run(ctx context.Context) error {
	f.log.Info("trading floor starting",
		"desks", len(f.desks),
	)

	signals := f.wire.Subscribe()
	f.startWorkers(ctx)

	// Start the wire after subscribers are ready so initial feed bursts are not lost.
	if err := f.wire.Start(ctx); err != nil {
		return err
	}

	f.log.Info("trading floor running — processing signals")

	for {
		select {
		case <-ctx.Done():
			f.log.Info("trading floor shutting down",
				"signals_processed", f.signalsProcessed.Load(),
				"trades_executed", f.tradesExecuted.Load(),
				"tasks_enqueued", f.tasksEnqueued.Load(),
				"tasks_completed", f.tasksCompleted.Load(),
				"tasks_dropped", f.tasksDropped.Load(),
				"queue_depth", len(f.taskQueue),
			)
			return ctx.Err()

		case sig, ok := <-signals:
			if !ok {
				return nil
			}
			f.signalsProcessed.Add(1)
			f.persistSignal(ctx, sig)
			f.log.Debug("signal received",
				"signal_id", sig.ID,
				"source", sig.Source,
				"type", sig.Type,
				"category", sig.Category,
				"urgency", sig.Urgency,
			)
			f.fanOut(ctx, sig)
		}
	}
}

func (f *Floor) startWorkers(ctx context.Context) {
	f.once.Do(func() {
		for i := 0; i < f.workerCount; i++ {
			workerID := i + 1
			observe.SafeGo(f.log, "desk worker panic", func() {
				f.workerLoop(ctx, workerID)
			}, "worker_id", workerID)
		}
		f.log.Info("desk workers started",
			"workers", f.workerCount,
			"queue_capacity", cap(f.taskQueue),
			"enqueue_timeout", f.enqueueTimeout,
			"slow_task_warn_at", f.slowTaskWarnAt,
		)
	})
}

func (f *Floor) workerLoop(ctx context.Context, workerID int) {
	log := f.log.With("worker_id", workerID)
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-f.taskQueue:
			started := time.Now()
			f.tasksStarted.Add(1)
			f.activeTasks.Add(1)

			span := trace.New(f.sessionID, task.desk.ID, task.sig.ID)
			deskCtx := trace.IntoContext(ctx, span)
			task.desk.Process(deskCtx, task.sig)

			f.activeTasks.Add(-1)
			f.tasksCompleted.Add(1)

			duration := time.Since(started).Round(time.Millisecond)
			if duration >= f.slowTaskWarnAt {
				log.Warn("desk task slow",
					"desk_id", task.desk.ID,
					"signal_id", task.sig.ID,
					"source", task.sig.Source,
					"duration", duration.String(),
				)
				continue
			}
			log.Debug("desk task complete",
				"desk_id", task.desk.ID,
				"signal_id", task.sig.ID,
				"source", task.sig.Source,
				"duration", duration.String(),
			)
		}
	}
}

// fanOut routes a signal only to relevant desks and queues the work.
func (f *Floor) fanOut(ctx context.Context, sig signal.Signal) {
	f.mu.RLock()
	desks := f.desks
	f.mu.RUnlock()

	relevantDomains := relevantDomainsForSignal(sig)
	routed := 0
	skipped := 0
	dropped := 0

	for _, desk := range desks {
		if originDesk := internalOriginDesk(sig); originDesk != "" && originDesk == desk.ID {
			skipped++
			continue
		}
		if !domainShouldReviewSignal(desk.Domain, sig) {
			skipped++
			continue
		}
		if !f.enqueueTask(ctx, deskTask{desk: desk, sig: sig}) {
			dropped++
			continue
		}
		routed++
	}

	f.tasksSkipped.Add(int64(skipped))

	if routed == 0 {
		f.log.Warn("signal routed to no desks",
			"signal_id", sig.ID,
			"source", sig.Source,
			"category", sig.Category,
			"type", sig.Type,
			"candidate_domains", relevantDomains,
			"skipped", skipped,
		)
		return
	}

	if dropped > 0 {
		f.log.Warn("signal desk fanout dropped tasks",
			"signal_id", sig.ID,
			"source", sig.Source,
			"category", sig.Category,
			"routed", routed,
			"skipped", skipped,
			"dropped", dropped,
			"queue_depth", len(f.taskQueue),
			"candidate_domains", relevantDomains,
		)
		return
	}

	f.log.Debug("signal enqueued for desks",
		"signal_id", sig.ID,
		"source", sig.Source,
		"category", sig.Category,
		"routed", routed,
		"skipped", skipped,
		"queue_depth", len(f.taskQueue),
		"candidate_domains", relevantDomains,
	)
}

func (f *Floor) enqueueTask(ctx context.Context, task deskTask) bool {
	timer := time.NewTimer(f.enqueueTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case f.taskQueue <- task:
		f.tasksEnqueued.Add(1)
		return true
	case <-timer.C:
		f.tasksDropped.Add(1)
		return false
	}
}

func (f *Floor) persistSignal(ctx context.Context, sig signal.Signal) {
	f.mu.RLock()
	db := f.store
	graph := f.graph
	f.mu.RUnlock()

	if db == nil && graph == nil {
		return
	}
	if db != nil {
		if err := db.UpsertSignal(ctx, sig); err != nil {
			if errors.Is(err, store.ErrDuplicateSignalContentHash) {
				f.log.Debug("ingress signal content duplicate",
					"signal_id", sig.ID,
					"source", sig.Source,
				)
				return
			}
			f.log.Warn("persist ingress signal failed",
				"signal_id", sig.ID,
				"source", sig.Source,
				"error", err,
			)
		}
	}
	if err := graph.UpsertSignal(ctx, sig); err != nil {
		f.log.Warn("persist ingress signal to graph failed",
			"signal_id", sig.ID,
			"source", sig.Source,
			"error", err,
		)
	}
	f.log.Debug("ingress signal persisted", "signal_id", sig.ID, "source", sig.Source)
}

// Stats returns floor-level metrics
func (f *Floor) Stats() FloorStats {
	wireStats := f.wire.Stats()
	return FloorStats{
		Desks:            len(f.desks),
		Workers:          f.workerCount,
		SignalsProcessed: f.signalsProcessed.Load(),
		TradesExecuted:   f.tradesExecuted.Load(),
		TasksEnqueued:    f.tasksEnqueued.Load(),
		TasksStarted:     f.tasksStarted.Load(),
		TasksCompleted:   f.tasksCompleted.Load(),
		TasksDropped:     f.tasksDropped.Load(),
		TasksSkipped:     f.tasksSkipped.Load(),
		ActiveTasks:      f.activeTasks.Load(),
		TaskQueueDepth:   len(f.taskQueue),
		TaskQueueCap:     cap(f.taskQueue),
		WireStats:        wireStats,
	}
}

func (f *Floor) RecordTrade() {
	f.tradesExecuted.Add(1)
}

type FloorStats struct {
	Desks            int
	Workers          int
	SignalsProcessed int64
	TradesExecuted   int64
	TasksEnqueued    int64
	TasksStarted     int64
	TasksCompleted   int64
	TasksDropped     int64
	TasksSkipped     int64
	ActiveTasks      int64
	TaskQueueDepth   int
	TaskQueueCap     int
	WireStats        wire.WireStats
}

func readEnvInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func readEnvDuration(name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
