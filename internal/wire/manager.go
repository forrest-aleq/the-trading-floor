package wire

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

type queuedDelivery struct {
	sub chan signal.Signal
	sig signal.Signal
}

// Manager is the central signal bus. All sources fan in, all desks fan out.
type Manager struct {
	log         *slog.Logger
	feeds       []Feed
	subscribers []chan signal.Signal
	mu          sync.RWMutex

	overflowMu     sync.Mutex
	overflow       []queuedDelivery
	overflowNotify chan struct{}

	deduper         *Deduper
	clusterer       *Clusterer
	crossReferencer *CrossReferencer

	// Metrics
	totalReceived     atomic.Int64
	totalDeduped      atomic.Int64
	totalFanout       atomic.Int64
	totalCorroborated atomic.Int64
	totalOverflowed   atomic.Int64
	totalReplayed     atomic.Int64
	totalDropped      atomic.Int64

	// Config
	bufferSize    int
	maxOverflow   int
	retryInterval time.Duration
}

// Feed is any signal source
type Feed interface {
	Name() string
	Start(ctx context.Context, out chan<- signal.Signal) error
}

func NewManager() *Manager {
	return &Manager{
		log:             slog.Default().With("component", "wire"),
		bufferSize:      10000,
		maxOverflow:     50000,
		retryInterval:   50 * time.Millisecond,
		overflowNotify:  make(chan struct{}, 1),
		deduper:         NewDeduper(2048, 0.92),
		clusterer:       NewClusterer(1024, 0.88),
		crossReferencer: NewCrossReferencer(4096, 16),
	}
}

// RegisterFeed adds a signal source
func (m *Manager) RegisterFeed(feed Feed) {
	m.feeds = append(m.feeds, feed)
	m.log.Info("feed registered", "name", feed.Name())
}

// Subscribe returns a channel that receives all signals
func (m *Manager) Subscribe() <-chan signal.Signal {
	ch := make(chan signal.Signal, m.bufferSize)
	m.mu.Lock()
	m.subscribers = append(m.subscribers, ch)
	m.mu.Unlock()
	return ch
}

// Start begins all feeds and fans out signals to subscribers
func (m *Manager) Start(ctx context.Context) error {
	// Central channel all feeds write to
	ingress := make(chan signal.Signal, m.bufferSize)

	// Start all feeds
	for _, feed := range m.feeds {
		f := feed
		go func() {
			m.log.Info("starting feed", "name", f.Name())
			if err := f.Start(ctx, ingress); err != nil {
				if errors.Is(err, context.Canceled) {
					m.log.Info("feed stopped", "name", f.Name())
					return
				}
				m.log.Error("feed error", "name", f.Name(), "error", err)
			}
		}()
	}

	// Fan-out loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-ingress:
				if !ok {
					return
				}
				m.totalReceived.Add(1)
				sig = NormalizeSignal(sig)

				if m.deduper != nil && m.deduper.IsDuplicate(sig) {
					m.totalDeduped.Add(1)
					continue
				}
				if m.clusterer != nil {
					sig = m.clusterer.Assign(sig)
				}
				if m.crossReferencer != nil {
					sig = m.crossReferencer.Enrich(sig)
				}
				if len(sig.CorroboratingSources) > 0 {
					m.totalCorroborated.Add(1)
				}

				// Fan out to all subscribers
				m.mu.RLock()
				for _, sub := range m.subscribers {
					select {
					case sub <- sig:
						m.totalFanout.Add(1)
					default:
						if m.enqueueOverflow(sub, sig) {
							m.log.Warn("subscriber buffer full, queued for replay",
								"source", sig.Source,
								"type", sig.Type,
							)
							continue
						}
						m.totalDropped.Add(1)
						m.log.Error("subscriber buffer full, overflow queue exhausted",
							"source", sig.Source,
							"type", sig.Type,
						)
					}
				}
				m.mu.RUnlock()
			}
		}
	}()

	go m.drainOverflow(ctx)

	m.log.Info("wire started", "feeds", len(m.feeds), "subscribers", len(m.subscribers))
	return nil
}

// Stats returns current wire metrics
func (m *Manager) Stats() WireStats {
	m.overflowMu.Lock()
	pendingOverflow := len(m.overflow)
	m.overflowMu.Unlock()

	return WireStats{
		TotalReceived:     m.totalReceived.Load(),
		TotalDeduped:      m.totalDeduped.Load(),
		TotalFanout:       m.totalFanout.Load(),
		TotalCorroborated: m.totalCorroborated.Load(),
		TotalOverflowed:   m.totalOverflowed.Load(),
		TotalReplayed:     m.totalReplayed.Load(),
		TotalDropped:      m.totalDropped.Load(),
		PendingOverflow:   pendingOverflow,
		ActiveFeeds:       len(m.feeds),
		Subscribers:       len(m.subscribers),
	}
}

type WireStats struct {
	TotalReceived     int64
	TotalDeduped      int64
	TotalFanout       int64
	TotalCorroborated int64
	TotalOverflowed   int64
	TotalReplayed     int64
	TotalDropped      int64
	PendingOverflow   int
	ActiveFeeds       int
	Subscribers       int
}

func (m *Manager) enqueueOverflow(sub chan signal.Signal, sig signal.Signal) bool {
	m.overflowMu.Lock()
	defer m.overflowMu.Unlock()

	if m.maxOverflow > 0 && len(m.overflow) >= m.maxOverflow {
		return false
	}

	m.overflow = append(m.overflow, queuedDelivery{
		sub: sub,
		sig: sig,
	})
	m.totalOverflowed.Add(1)

	select {
	case m.overflowNotify <- struct{}{}:
	default:
	}

	return true
}

func (m *Manager) drainOverflow(ctx context.Context) {
	ticker := time.NewTicker(m.retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.overflowNotify:
		case <-ticker.C:
		}

		m.overflowMu.Lock()
		if len(m.overflow) == 0 {
			m.overflowMu.Unlock()
			continue
		}

		pending := m.overflow
		remaining := m.overflow[:0]
		for _, delivery := range pending {
			select {
			case delivery.sub <- delivery.sig:
				m.totalFanout.Add(1)
				m.totalReplayed.Add(1)
			default:
				remaining = append(remaining, delivery)
			}
		}

		if len(remaining) == 0 {
			m.overflow = nil
		} else {
			m.overflow = remaining
		}
		m.overflowMu.Unlock()
	}
}
