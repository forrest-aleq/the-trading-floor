package ibkr

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// PacingBudget enforces IBKR's rate limits centrally.
// IB Gateway limits: 50 messages/second, max 15 simultaneous market data lines.
type PacingBudget struct {
	mu  sync.Mutex
	log *slog.Logger

	// Token bucket for API messages (50/sec)
	msgTokens   int
	msgMax      int
	msgRefill   *time.Ticker
	msgRefillCh <-chan time.Time

	// Market data line tracking
	activeLines int
	maxLines    int

	// Contract qualification cache to reduce API calls
	qualifyCache map[string]time.Time
	cacheTTL     time.Duration
}

func NewPacingBudget() *PacingBudget {
	ticker := time.NewTicker(time.Second)
	return &PacingBudget{
		log:          slog.Default().With("component", "pacing"),
		msgTokens:    50,
		msgMax:       50,
		msgRefill:    ticker,
		msgRefillCh:  ticker.C,
		activeLines:  0,
		maxLines:     15,
		qualifyCache: make(map[string]time.Time),
		cacheTTL:     5 * time.Minute,
	}
}

// Run refills message tokens once per second. Must be called in a goroutine.
func (p *PacingBudget) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			p.msgRefill.Stop()
			return
		case <-p.msgRefillCh:
			p.mu.Lock()
			p.msgTokens = p.msgMax
			p.mu.Unlock()
		}
	}
}

// AcquireMessage blocks until a message token is available or ctx is cancelled.
func (p *PacingBudget) AcquireMessage(ctx context.Context) error {
	for {
		p.mu.Lock()
		if p.msgTokens > 0 {
			p.msgTokens--
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// AcquireMarketDataLine reserves a market data line. Returns false if at capacity.
func (p *PacingBudget) AcquireMarketDataLine() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.activeLines >= p.maxLines {
		p.log.Warn("market data line limit reached", "active", p.activeLines, "max", p.maxLines)
		return false
	}
	p.activeLines++
	return true
}

// ReleaseMarketDataLine frees a market data line.
func (p *PacingBudget) ReleaseMarketDataLine() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.activeLines > 0 {
		p.activeLines--
	}
}

// ActiveMarketDataLines returns current count.
func (p *PacingBudget) ActiveMarketDataLines() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activeLines
}

// ShouldQualify checks if a contract needs re-qualification.
func (p *PacingBudget) ShouldQualify(symbol string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if t, ok := p.qualifyCache[symbol]; ok {
		if time.Since(t) < p.cacheTTL {
			return false
		}
	}
	return true
}

// RecordQualify marks a contract as recently qualified.
func (p *PacingBudget) RecordQualify(symbol string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.qualifyCache[symbol] = time.Now()
}

// Stats returns current pacing state for observability.
func (p *PacingBudget) Stats() PacingStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PacingStats{
		MsgTokensAvailable:   p.msgTokens,
		MsgTokensMax:         p.msgMax,
		ActiveMarketDataLines: p.activeLines,
		MaxMarketDataLines:   p.maxLines,
		CachedContracts:      len(p.qualifyCache),
	}
}

type PacingStats struct {
	MsgTokensAvailable    int `json:"msg_tokens_available"`
	MsgTokensMax          int `json:"msg_tokens_max"`
	ActiveMarketDataLines int `json:"active_market_data_lines"`
	MaxMarketDataLines    int `json:"max_market_data_lines"`
	CachedContracts       int `json:"cached_contracts"`
}
