package marketdata

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

// Subscriber receives price updates.
type Subscriber func(prices map[string]float64)

// Manager is a centralized market data subscription manager.
// Instead of each desk polling IBKR independently, this manager
// maintains a shared watchlist and distributes updates to subscribers.
type Manager struct {
	log      *slog.Logger
	client   MarketDataClient
	pacing   *ibkr.PacingBudget
	interval time.Duration

	mu            sync.RWMutex
	watchlist     map[string]model.Instrument // symbol -> instrument
	prices        map[string]float64          // symbol -> last price
	suppressUntil map[string]time.Time
	subscribers   []Subscriber
}

// MarketDataClient is the interface for fetching market data.
type MarketDataClient interface {
	ReqMarketData(context.Context, model.Instrument) (*ibkr.MarketData, error)
}

// NewManager creates a new centralized market data manager.
func NewManager(client MarketDataClient, pacing *ibkr.PacingBudget, interval time.Duration) *Manager {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Manager{
		log:           slog.Default().With("component", "marketdata"),
		client:        client,
		pacing:        pacing,
		interval:      interval,
		watchlist:     make(map[string]model.Instrument),
		prices:        make(map[string]float64),
		suppressUntil: make(map[string]time.Time),
	}
}

// AddInstruments adds instruments to the shared watchlist.
func (m *Manager) AddInstruments(instruments []model.Instrument) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range instruments {
		m.watchlist[inst.Symbol] = inst
	}
}

// Subscribe registers a callback for price updates.
func (m *Manager) Subscribe(fn Subscriber) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribers = append(m.subscribers, fn)
}

// LatestPrices returns the most recent snapshot of prices.
func (m *Manager) LatestPrices() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make(map[string]float64, len(m.prices))
	for k, v := range m.prices {
		cp[k] = v
	}
	return cp
}

// Run polls IBKR for market data on the shared watchlist and distributes updates.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

func (m *Manager) poll(ctx context.Context) {
	m.mu.RLock()
	instruments := make([]model.Instrument, 0, len(m.watchlist))
	for _, inst := range m.watchlist {
		instruments = append(instruments, inst)
	}
	m.mu.RUnlock()

	if len(instruments) == 0 {
		return
	}

	prices := make(map[string]float64)
	for _, inst := range instruments {
		if ctx.Err() != nil {
			return
		}
		if m.shouldSuppress(inst.Symbol, time.Now()) {
			continue
		}

		// Respect pacing budget
		if m.pacing != nil {
			if err := m.pacing.AcquireMessage(ctx); err != nil {
				return
			}
		}

		md, err := m.client.ReqMarketData(ctx, inst)
		if err != nil {
			backoff := marketDataBackoff(err, m.interval)
			m.suppress(inst.Symbol, time.Now().Add(backoff))
			m.log.Warn("market data fetch failed", "symbol", inst.Symbol, "error", err, "retry_after", backoff)
			continue
		}
		m.clearSuppression(inst.Symbol)

		price := bestPrice(md)
		if price > 0 {
			prices[inst.Symbol] = price
		}
	}

	if len(prices) == 0 {
		return
	}

	m.mu.Lock()
	for k, v := range prices {
		m.prices[k] = v
	}
	subs := make([]Subscriber, len(m.subscribers))
	copy(subs, m.subscribers)
	m.mu.Unlock()

	for _, fn := range subs {
		fn(prices)
	}
}

func bestPrice(md *ibkr.MarketData) float64 {
	switch {
	case md.Last > 0:
		return md.Last
	case md.Bid > 0 && md.Ask > 0:
		return (md.Bid + md.Ask) / 2
	case md.Bid > 0:
		return md.Bid
	case md.Ask > 0:
		return md.Ask
	default:
		return 0
	}
}

func (m *Manager) shouldSuppress(symbol string, now time.Time) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	until, ok := m.suppressUntil[symbol]
	return ok && now.Before(until)
}

func (m *Manager) suppress(symbol string, until time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.suppressUntil[symbol] = until
}

func (m *Manager) clearSuppression(symbol string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.suppressUntil, symbol)
}

func marketDataBackoff(err error, interval time.Duration) time.Duration {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not subscribed"),
		strings.Contains(message, "additional subscription"),
		strings.Contains(message, "delayed market data is available"):
		return 10 * time.Minute
	default:
		return interval
	}
}
