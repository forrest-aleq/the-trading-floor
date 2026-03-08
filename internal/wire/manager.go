package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"

	"github.com/hnic/trading-floor/pkg/signal"
)

// Manager is the central signal bus. All sources fan in, all desks fan out.
type Manager struct {
	log         *slog.Logger
	feeds       []Feed
	subscribers []chan signal.Signal
	mu          sync.RWMutex

	// Dedup
	seenHashes sync.Map // content_hash → struct{}

	// Metrics
	totalReceived int64
	totalDeduped  int64
	totalFanout   int64

	// Config
	bufferSize int
}

// Feed is any signal source
type Feed interface {
	Name() string
	Start(ctx context.Context, out chan<- signal.Signal) error
}

func NewManager() *Manager {
	return &Manager{
		log:        slog.Default().With("component", "wire"),
		bufferSize: 10000,
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
			case sig := <-ingress:
				m.totalReceived++

				// Dedup: content hash check
				if sig.ContentHash == "" {
					sig.ContentHash = hashContent(sig)
				}
				if _, loaded := m.seenHashes.LoadOrStore(sig.ContentHash, struct{}{}); loaded {
					m.totalDeduped++
					continue
				}

				// Fan out to all subscribers
				m.mu.RLock()
				for _, sub := range m.subscribers {
					select {
					case sub <- sig:
						m.totalFanout++
					default:
						// Subscriber buffer full — signal dropped for this subscriber
						m.log.Warn("subscriber buffer full, signal dropped",
							"source", sig.Source,
							"type", sig.Type,
						)
					}
				}
				m.mu.RUnlock()
			}
		}
	}()

	m.log.Info("wire started", "feeds", len(m.feeds), "subscribers", len(m.subscribers))
	return nil
}

// Stats returns current wire metrics
func (m *Manager) Stats() WireStats {
	return WireStats{
		TotalReceived: m.totalReceived,
		TotalDeduped:  m.totalDeduped,
		TotalFanout:   m.totalFanout,
		ActiveFeeds:   len(m.feeds),
		Subscribers:   len(m.subscribers),
	}
}

type WireStats struct {
	TotalReceived int64
	TotalDeduped  int64
	TotalFanout   int64
	ActiveFeeds   int
	Subscribers   int
}

func hashContent(sig signal.Signal) string {
	h := sha256.New()
	h.Write([]byte(sig.Source))
	h.Write([]byte(string(sig.Type)))
	// Normalize: lowercase, trim whitespace
	content := strings.ToLower(strings.TrimSpace(string(sig.Raw)))
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}
