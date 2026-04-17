package marketdata

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

// Subscriber receives price updates.
type Subscriber func(prices map[string]float64)

type PricePoint struct {
	ObservedAt time.Time
	Price      float64
}

type QuoteFreshnessReport struct {
	AsOf      time.Time     `json:"as_of"`
	Total     int           `json:"total"`
	Fresh     int           `json:"fresh"`
	Stale     int           `json:"stale"`
	Missing   int           `json:"missing"`
	NewestAge time.Duration `json:"newest_age,omitempty"`
	OldestAge time.Duration `json:"oldest_age,omitempty"`
}

const maxHistoryPointsPerInstrument = 256

// Manager is a centralized market data subscription manager.
// Instead of each desk polling IBKR independently, this manager
// maintains a shared watchlist and distributes updates to subscribers.
type Manager struct {
	log      *slog.Logger
	client   SnapshotProvider
	budget   RequestBudget
	interval time.Duration

	mu            sync.RWMutex
	watchlist     map[string]model.Instrument // instrument key -> instrument
	prices        map[string]float64          // instrument key -> last price
	quotes        map[string]model.MarketQuote
	history       map[string][]PricePoint // instrument key/symbol -> rolling history
	suppressUntil map[string]time.Time
	subscribers   []Subscriber
}

// NewManager creates a new centralized market data manager.
func NewManager(client SnapshotProvider, budget RequestBudget, interval time.Duration) *Manager {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Manager{
		log:           slog.Default().With("component", "marketdata"),
		client:        client,
		budget:        budget,
		interval:      interval,
		watchlist:     make(map[string]model.Instrument),
		prices:        make(map[string]float64),
		quotes:        make(map[string]model.MarketQuote),
		history:       make(map[string][]PricePoint),
		suppressUntil: make(map[string]time.Time),
	}
}

// AddInstruments adds instruments to the shared watchlist.
func (m *Manager) AddInstruments(instruments []model.Instrument) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range instruments {
		if inst.Symbol == "" {
			continue
		}
		m.watchlist[inst.Key()] = inst
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

func (m *Manager) LatestPrice(inst model.Instrument) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if price, ok := m.prices[inst.Key()]; ok && price > 0 {
		return price, true
	}
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if price, ok := m.prices[symbol]; ok && price > 0 {
		return price, true
	}
	return 0, false
}

func (m *Manager) LatestQuote(inst model.Instrument) (model.MarketQuote, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if quote, ok := m.quotes[inst.Key()]; ok && quote.ReferencePrice() > 0 {
		return quote, true
	}
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if quote, ok := m.quotes[symbol]; ok && quote.ReferencePrice() > 0 {
		return quote, true
	}
	return model.MarketQuote{}, false
}

func (m *Manager) FreshnessReport(instruments []model.Instrument, now time.Time, maxAge time.Duration) QuoteFreshnessReport {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := QuoteFreshnessReport{
		AsOf:  now,
		Total: len(instruments),
	}
	if len(instruments) == 0 {
		return report
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, inst := range instruments {
		quote, ok := m.lookupQuoteLocked(inst)
		if !ok || quote.ReferencePrice() <= 0 {
			report.Missing++
			continue
		}

		observedAt := quote.ObservedAt
		if observedAt.IsZero() {
			report.Missing++
			continue
		}

		age := now.Sub(observedAt.UTC())
		if age < 0 {
			age = 0
		}
		if report.Fresh+report.Stale == 0 || age < report.NewestAge {
			report.NewestAge = age
		}
		if age > report.OldestAge {
			report.OldestAge = age
		}

		if maxAge > 0 && age > maxAge {
			report.Stale++
			continue
		}
		report.Fresh++
	}

	return report
}

// BestEffortPrice resolves the best available price for an instrument using the
// current cache first, then same-symbol watchlist candidates, then historical
// bars from the broker if needed.
func (m *Manager) BestEffortPrice(ctx context.Context, inst model.Instrument) (model.Instrument, float64, bool) {
	candidates := m.instrumentCandidates(inst)
	for _, candidate := range candidates {
		if price, ok := m.cachedPrice(candidate); ok && price > 0 {
			return candidate, price, true
		}
	}
	for _, candidate := range candidates {
		if price, ok := m.historicalFallbackPrice(ctx, candidate); ok && price > 0 {
			m.mu.Lock()
			m.prices[candidate.Key()] = price
			symbol := strings.ToUpper(strings.TrimSpace(candidate.Symbol))
			if symbol != "" {
				m.prices[symbol] = price
			}
			now := time.Now().UTC()
			m.appendHistoryLocked(candidate.Key(), price, now)
			if symbol != "" {
				m.appendHistoryLocked(symbol, price, now)
			}
			m.mu.Unlock()
			return candidate, price, true
		}
	}
	return model.Instrument{}, 0, false
}

func (m *Manager) BestEffortQuote(ctx context.Context, inst model.Instrument) (model.Instrument, model.MarketQuote, bool) {
	candidates := m.instrumentCandidates(inst)
	for _, candidate := range candidates {
		if quote, ok := m.cachedQuote(candidate); ok && quote.ReferencePrice() > 0 {
			return candidate, quote, true
		}
	}
	for _, candidate := range candidates {
		quote, ok := m.liveQuote(ctx, candidate)
		if !ok || quote.ReferencePrice() <= 0 {
			continue
		}
		m.storeQuote(candidate, quote)
		return candidate, quote, true
	}
	return model.Instrument{}, model.MarketQuote{}, false
}

func (m *Manager) PriceChange(inst model.Instrument, window time.Duration) (float64, bool) {
	if window <= 0 {
		return 0, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	history := m.lookupHistoryLocked(inst)
	if len(history) < 2 {
		return 0, false
	}

	latest := history[len(history)-1]
	cutoff := latest.ObservedAt.Add(-window)
	baseline := PricePoint{}
	found := false
	for i := len(history) - 1; i >= 0; i-- {
		point := history[i]
		if point.ObservedAt.Before(cutoff) || point.ObservedAt.Equal(cutoff) {
			baseline = point
			found = true
			break
		}
	}
	if !found {
		baseline = history[0]
		if latest.ObservedAt.Sub(baseline.ObservedAt) < window/2 {
			return 0, false
		}
	}
	if baseline.Price <= 0 || latest.Price <= 0 {
		return 0, false
	}
	return ((latest.Price - baseline.Price) / baseline.Price) * 100, true
}

func (m *Manager) RealizedVolatility(inst model.Instrument, window time.Duration) (float64, bool) {
	if window <= 0 {
		return 0, false
	}

	m.mu.RLock()
	history := append([]PricePoint(nil), m.lookupHistoryLocked(inst)...)
	m.mu.RUnlock()
	if len(history) < 3 {
		return 0, false
	}

	latest := history[len(history)-1]
	cutoff := latest.ObservedAt.Add(-window)
	points := make([]PricePoint, 0, len(history))
	for _, point := range history {
		if point.Price <= 0 {
			continue
		}
		if point.ObservedAt.Before(cutoff) {
			continue
		}
		points = append(points, point)
	}
	if len(points) < 3 {
		return 0, false
	}

	return realizedVolatility(points)
}

// Run polls IBKR for market data on the shared watchlist and distributes updates.
func (m *Manager) Run(ctx context.Context) {
	if m == nil {
		<-ctx.Done()
		return
	}
	if m.client == nil {
		m.log.Info("market state manager running cache-only; no live provider configured")
		<-ctx.Done()
		return
	}

	m.poll(ctx)

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
	if m == nil || m.client == nil {
		return
	}

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
	quotes := make(map[string]model.MarketQuote)
	timestamp := time.Now().UTC()
	for _, inst := range instruments {
		if ctx.Err() != nil {
			return
		}
		key := inst.Key()
		if m.shouldSuppress(key, time.Now()) {
			continue
		}

		// Respect pacing budget
		if m.budget != nil {
			if err := m.budget.Acquire(ctx); err != nil {
				return
			}
		}

		snapshot, err := m.client.Snapshot(ctx, inst)
		if err != nil || snapshot == nil {
			if fallback, ok := m.historicalFallbackPrice(ctx, inst); ok {
				prices[key] = fallback
				continue
			}
			backoff := marketDataBackoff(err, m.interval)
			m.suppress(key, time.Now().Add(backoff))
			m.log.Warn("market data fetch failed", "symbol", inst.Label(), "error", err, "retry_after", backoff)
			continue
		}
		m.clearSuppression(key)

		price := bestPrice(snapshot)
		if price <= 0 {
			if fallback, ok := m.historicalFallbackPrice(ctx, inst); ok {
				prices[key] = fallback
			}
			continue
		}
		prices[key] = price
		quoteTime := timestamp
		if !snapshot.ObservedAt.IsZero() {
			quoteTime = snapshot.ObservedAt.UTC()
		}
		quotes[key] = model.MarketQuote{
			ObservedAt: quoteTime,
			Last:       snapshot.Last,
			Bid:        snapshot.Bid,
			Ask:        snapshot.Ask,
			Volume:     snapshot.Volume,
		}
	}

	if len(prices) == 0 {
		return
	}

	m.mu.Lock()
	for k, v := range prices {
		m.prices[k] = v
	}
	for _, inst := range instruments {
		key := inst.Key()
		price, ok := prices[key]
		if !ok || price <= 0 {
			continue
		}
		m.appendHistoryLocked(key, price, timestamp)
		if quote, ok := quotes[key]; ok && quote.ReferencePrice() > 0 {
			m.quotes[key] = quote
		}
		symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
		if symbol != "" {
			m.prices[symbol] = price
			m.appendHistoryLocked(symbol, price, timestamp)
			if quote, ok := quotes[key]; ok && quote.ReferencePrice() > 0 {
				m.quotes[symbol] = quote
			}
		}
	}
	subs := make([]Subscriber, len(m.subscribers))
	copy(subs, m.subscribers)
	m.mu.Unlock()

	for _, fn := range subs {
		fn(prices)
	}
}

func (m *Manager) historicalFallbackPrice(ctx context.Context, inst model.Instrument) (float64, bool) {
	client, ok := m.client.(HistoricalProvider)
	if !ok || inst.Symbol == "" {
		return 0, false
	}
	historyCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	bars, err := client.HistoricalBars(historyCtx, inst, time.Now(), "2 D", "1 hour", "", true)
	if err != nil || len(bars) == 0 {
		return 0, false
	}
	for i := len(bars) - 1; i >= 0; i-- {
		if bars[i].Close > 0 {
			return bars[i].Close, true
		}
	}
	return 0, false
}

func bestPrice(snapshot *Snapshot) float64 {
	switch {
	case snapshot == nil:
		return 0
	case snapshot.Last > 0:
		return snapshot.Last
	case snapshot.Bid > 0 && snapshot.Ask > 0:
		return (snapshot.Bid + snapshot.Ask) / 2
	case snapshot.Bid > 0:
		return snapshot.Bid
	case snapshot.Ask > 0:
		return snapshot.Ask
	default:
		return 0
	}
}

func (m *Manager) shouldSuppress(key string, now time.Time) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	until, ok := m.suppressUntil[key]
	return ok && now.Before(until)
}

func (m *Manager) suppress(key string, until time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.suppressUntil[key] = until
}

func (m *Manager) clearSuppression(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.suppressUntil, key)
}

func marketDataBackoff(err error, interval time.Duration) time.Duration {
	if err == nil {
		return interval
	}
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

func (m *Manager) appendHistoryLocked(key string, price float64, observedAt time.Time) {
	if key == "" || price <= 0 {
		return
	}
	history := append(m.history[key], PricePoint{
		ObservedAt: observedAt,
		Price:      price,
	})
	if len(history) > maxHistoryPointsPerInstrument {
		history = history[len(history)-maxHistoryPointsPerInstrument:]
	}
	m.history[key] = history
}

func (m *Manager) lookupHistoryLocked(inst model.Instrument) []PricePoint {
	if history := m.history[inst.Key()]; len(history) > 0 {
		return history
	}
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if symbol == "" {
		return nil
	}
	return m.history[symbol]
}

func (m *Manager) lookupQuoteLocked(inst model.Instrument) (model.MarketQuote, bool) {
	if quote, ok := m.quotes[inst.Key()]; ok && quote.ReferencePrice() > 0 {
		return quote, true
	}
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if quote, ok := m.quotes[symbol]; ok && quote.ReferencePrice() > 0 {
		return quote, true
	}
	return model.MarketQuote{}, false
}

func (m *Manager) instrumentCandidates(inst model.Instrument) []model.Instrument {
	if inst.Symbol == "" {
		return nil
	}

	seen := make(map[string]struct{})
	candidates := make([]model.Instrument, 0, 4)
	appendCandidate := func(candidate model.Instrument) {
		if candidate.Symbol == "" {
			return
		}
		key := candidate.Key()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, candidate)
	}

	m.mu.RLock()
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	for _, watch := range m.watchlist {
		if strings.ToUpper(strings.TrimSpace(watch.Symbol)) == symbol {
			appendCandidate(watch)
		}
	}
	m.mu.RUnlock()
	appendCandidate(inst)
	return candidates
}

func (m *Manager) cachedPrice(inst model.Instrument) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if price, ok := m.prices[inst.Key()]; ok && price > 0 {
		return price, true
	}
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if symbol == "" {
		return 0, false
	}
	if price, ok := m.prices[symbol]; ok && price > 0 {
		return price, true
	}
	return 0, false
}

func (m *Manager) cachedQuote(inst model.Instrument) (model.MarketQuote, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if quote, ok := m.quotes[inst.Key()]; ok && quote.ReferencePrice() > 0 {
		return quote, true
	}
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if symbol == "" {
		return model.MarketQuote{}, false
	}
	if quote, ok := m.quotes[symbol]; ok && quote.ReferencePrice() > 0 {
		return quote, true
	}
	return model.MarketQuote{}, false
}

func (m *Manager) liveQuote(ctx context.Context, inst model.Instrument) (model.MarketQuote, bool) {
	if m == nil || m.client == nil || inst.Symbol == "" {
		return model.MarketQuote{}, false
	}
	if m.budget != nil {
		if err := m.budget.Acquire(ctx); err != nil {
			return model.MarketQuote{}, false
		}
	}
	snapshot, err := m.client.Snapshot(ctx, inst)
	if err != nil || snapshot == nil {
		return model.MarketQuote{}, false
	}
	quoteTime := time.Now().UTC()
	if !snapshot.ObservedAt.IsZero() {
		quoteTime = snapshot.ObservedAt.UTC()
	}
	quote := model.MarketQuote{
		ObservedAt: quoteTime,
		Last:       snapshot.Last,
		Bid:        snapshot.Bid,
		Ask:        snapshot.Ask,
		Volume:     snapshot.Volume,
	}
	return quote, quote.ReferencePrice() > 0
}

// UpsertQuote allows external daemons to feed the local market-state cache
// without coupling deliberation-time code to live network requests.
func (m *Manager) UpsertQuote(inst model.Instrument, quote model.MarketQuote) {
	m.storeQuote(inst, quote)
}

// UpsertSnapshot allows external daemons to feed provider-neutral snapshots into
// the local cache.
func (m *Manager) UpsertSnapshot(inst model.Instrument, snapshot Snapshot) {
	if inst.Symbol == "" {
		return
	}
	quoteTime := snapshot.ObservedAt
	if quoteTime.IsZero() {
		quoteTime = time.Now().UTC()
	}
	m.storeQuote(inst, model.MarketQuote{
		ObservedAt: quoteTime,
		Last:       snapshot.Last,
		Bid:        snapshot.Bid,
		Ask:        snapshot.Ask,
		Volume:     snapshot.Volume,
	})
}

func (m *Manager) storeQuote(inst model.Instrument, quote model.MarketQuote) {
	if quote.ReferencePrice() <= 0 || inst.Symbol == "" {
		return
	}

	price := quote.ReferencePrice()
	observedAt := quote.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	key := inst.Key()
	m.quotes[key] = quote
	m.prices[key] = price
	m.appendHistoryLocked(key, price, observedAt)
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if symbol != "" {
		m.quotes[symbol] = quote
		m.prices[symbol] = price
		m.appendHistoryLocked(symbol, price, observedAt)
	}
}

func realizedVolatility(points []PricePoint) (float64, bool) {
	if len(points) < 3 {
		return 0, false
	}

	returns := make([]float64, 0, len(points)-1)
	totalIntervalSeconds := 0.0
	for i := 1; i < len(points); i++ {
		prev := points[i-1]
		curr := points[i]
		if prev.Price <= 0 || curr.Price <= 0 {
			continue
		}
		interval := curr.ObservedAt.Sub(prev.ObservedAt).Seconds()
		if interval <= 0 {
			continue
		}
		returns = append(returns, math.Log(curr.Price/prev.Price))
		totalIntervalSeconds += interval
	}
	if len(returns) < 2 || totalIntervalSeconds <= 0 {
		return 0, false
	}

	mean := 0.0
	for _, ret := range returns {
		mean += ret
	}
	mean /= float64(len(returns))

	variance := 0.0
	for _, ret := range returns {
		diff := ret - mean
		variance += diff * diff
	}
	variance /= float64(len(returns) - 1)
	if variance <= 0 {
		return 0, false
	}

	avgIntervalSeconds := totalIntervalSeconds / float64(len(returns))
	if avgIntervalSeconds <= 0 {
		return 0, false
	}
	annualization := math.Sqrt((365 * 24 * time.Hour).Seconds() / avgIntervalSeconds)
	return math.Sqrt(variance) * annualization * 100, true
}
