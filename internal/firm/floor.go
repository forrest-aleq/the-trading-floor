package firm

import (
	"context"
	"log/slog"
	"sync"

	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/pkg/signal"
)

// Floor is the main orchestrator — runs 24/7, fans signals to desks
type Floor struct {
	log   *slog.Logger
	wire  *wire.Manager
	desks []*Desk
	mu    sync.RWMutex

	// Metrics
	signalsProcessed int64
	tradesExecuted   int64
}

func NewFloor(wireMgr *wire.Manager) *Floor {
	return &Floor{
		log:  slog.Default().With("component", "floor"),
		wire: wireMgr,
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

// Run starts the floor — processes signals forever
func (f *Floor) Run(ctx context.Context) error {
	f.log.Info("trading floor starting",
		"desks", len(f.desks),
	)

	// Start the wire
	if err := f.wire.Start(ctx); err != nil {
		return err
	}

	// Subscribe to all signals
	signals := f.wire.Subscribe()

	f.log.Info("trading floor running — processing signals")

	for {
		select {
		case <-ctx.Done():
			f.log.Info("trading floor shutting down",
				"signals_processed", f.signalsProcessed,
				"trades_executed", f.tradesExecuted,
			)
			return ctx.Err()

		case sig, ok := <-signals:
			if !ok {
				return nil
			}
			f.signalsProcessed++
			f.fanOut(ctx, sig)
		}
	}
}

// fanOut sends a signal to every desk for parallel processing
func (f *Floor) fanOut(ctx context.Context, sig signal.Signal) {
	f.mu.RLock()
	desks := f.desks
	f.mu.RUnlock()

	for _, desk := range desks {
		d := desk // Capture for goroutine
		go func() {
			d.Process(ctx, sig)
		}()
	}
}

// Stats returns floor-level metrics
func (f *Floor) Stats() FloorStats {
	wireStats := f.wire.Stats()
	return FloorStats{
		Desks:            len(f.desks),
		SignalsProcessed: f.signalsProcessed,
		TradesExecuted:   f.tradesExecuted,
		WireStats:        wireStats,
	}
}

type FloorStats struct {
	Desks            int
	SignalsProcessed int64
	TradesExecuted   int64
	WireStats        wire.WireStats
}
