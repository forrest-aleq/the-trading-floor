package marketdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

type stubHistoricalClient struct {
	marketErr error
	md        *Snapshot
	bars      []HistoricalBar
}

func (s stubHistoricalClient) Snapshot(context.Context, model.Instrument) (*Snapshot, error) {
	if s.marketErr != nil {
		return nil, s.marketErr
	}
	if s.md != nil {
		return s.md, nil
	}
	return &Snapshot{}, nil
}

func (s stubHistoricalClient) HistoricalBars(context.Context, model.Instrument, time.Time, string, string, string, bool) ([]HistoricalBar, error) {
	return s.bars, nil
}

func TestMarketDataBackoffUsesLongerWindowForSubscriptionErrors(t *testing.T) {
	backoff := marketDataBackoff(errors.New("snapshot SPY: Requested market data requires additional subscription for API."), 30*time.Second)
	if backoff != 10*time.Minute {
		t.Fatalf("expected long backoff for subscription error, got %s", backoff)
	}

	backoff = marketDataBackoff(errors.New("temporary gateway hiccup"), 30*time.Second)
	if backoff != 30*time.Second {
		t.Fatalf("expected default backoff for transient error, got %s", backoff)
	}
}

func TestPriceChangeUsesRollingHistory(t *testing.T) {
	manager := NewManager(nil, nil, time.Minute)
	inst := model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"}

	manager.mu.Lock()
	manager.appendHistoryLocked(inst.Key(), 100, time.Now().Add(-70*time.Minute))
	manager.appendHistoryLocked(inst.Key(), 110, time.Now().Add(-10*time.Minute))
	manager.appendHistoryLocked("AAPL", 100, time.Now().Add(-70*time.Minute))
	manager.appendHistoryLocked("AAPL", 110, time.Now().Add(-10*time.Minute))
	manager.mu.Unlock()

	change, ok := manager.PriceChange(inst, time.Hour)
	if !ok {
		t.Fatal("expected price change to be available")
	}
	if change <= 0 {
		t.Fatalf("expected positive price change, got %.2f", change)
	}
}

func TestPollFallsBackToHistoricalPriceWhenLiveDataUnavailable(t *testing.T) {
	manager := NewManager(stubHistoricalClient{
		marketErr: errors.New("subscription missing"),
		bars: []HistoricalBar{
			{Time: time.Now().Add(-2 * time.Hour), Close: 98.5},
			{Time: time.Now().Add(-time.Hour), Close: 101.25},
		},
	}, nil, time.Minute)

	inst := model.Instrument{Symbol: "TLT", SecType: "STK", Currency: "USD", Exchange: "SMART"}
	manager.AddInstruments([]model.Instrument{inst})
	manager.poll(context.Background())

	price, ok := manager.LatestPrice(inst)
	if !ok {
		t.Fatal("expected historical fallback price to be recorded")
	}
	if price != 101.25 {
		t.Fatalf("expected latest historical close 101.25, got %.2f", price)
	}
}

func TestBestEffortPriceUsesSameSymbolWatchlistCandidate(t *testing.T) {
	manager := NewManager(stubHistoricalClient{
		marketErr: errors.New("subscription missing"),
		bars: []HistoricalBar{
			{Time: time.Now().Add(-2 * time.Hour), Close: 18.1},
			{Time: time.Now().Add(-time.Hour), Close: 18.4},
		},
	}, nil, time.Minute)

	manager.AddInstruments([]model.Instrument{{
		Symbol:   "VIX",
		SecType:  "IND",
		Currency: "USD",
		Exchange: "CBOE",
	}})

	resolved, price, ok := manager.BestEffortPrice(context.Background(), model.Instrument{
		Symbol:   "VIX",
		SecType:  "STK",
		Currency: "USD",
		Exchange: "SMART",
	})
	if !ok {
		t.Fatal("expected best-effort price to succeed")
	}
	if resolved.SecType != "IND" {
		t.Fatalf("expected resolved sec type IND, got %q", resolved.SecType)
	}
	if price != 18.4 {
		t.Fatalf("expected latest historical close 18.4, got %.2f", price)
	}
}

func TestPollRecordsLatestQuote(t *testing.T) {
	manager := NewManager(stubHistoricalClient{
		md: &Snapshot{
			ObservedAt: time.Now().UTC(),
			Last:       432.1,
			Bid:        432.0,
			Ask:        432.2,
			Volume:     1250000,
		},
	}, nil, time.Minute)

	inst := model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"}
	manager.AddInstruments([]model.Instrument{inst})
	manager.poll(context.Background())

	quote, ok := manager.LatestQuote(inst)
	if !ok {
		t.Fatal("expected latest quote to be recorded")
	}
	if quote.Last != 432.1 {
		t.Fatalf("expected last 432.1, got %.2f", quote.Last)
	}
	if quote.Bid != 432.0 || quote.Ask != 432.2 {
		t.Fatalf("unexpected quote %.2f / %.2f", quote.Bid, quote.Ask)
	}
	if quote.Volume != 1250000 {
		t.Fatalf("expected volume 1250000, got %d", quote.Volume)
	}
	if quote.SpreadBps() <= 0 {
		t.Fatalf("expected positive spread bps, got %.2f", quote.SpreadBps())
	}
}

func TestRealizedVolatilityUsesRollingHistory(t *testing.T) {
	manager := NewManager(nil, nil, time.Minute)
	inst := model.Instrument{Symbol: "QQQ", SecType: "STK", Currency: "USD"}
	now := time.Now().UTC()

	manager.mu.Lock()
	manager.appendHistoryLocked(inst.Key(), 100, now.Add(-4*time.Hour))
	manager.appendHistoryLocked(inst.Key(), 101.5, now.Add(-3*time.Hour))
	manager.appendHistoryLocked(inst.Key(), 99.8, now.Add(-2*time.Hour))
	manager.appendHistoryLocked(inst.Key(), 102.2, now.Add(-time.Hour))
	manager.appendHistoryLocked(inst.Key(), 101.1, now)
	manager.appendHistoryLocked("QQQ", 100, now.Add(-4*time.Hour))
	manager.appendHistoryLocked("QQQ", 101.5, now.Add(-3*time.Hour))
	manager.appendHistoryLocked("QQQ", 99.8, now.Add(-2*time.Hour))
	manager.appendHistoryLocked("QQQ", 102.2, now.Add(-time.Hour))
	manager.appendHistoryLocked("QQQ", 101.1, now)
	manager.mu.Unlock()

	vol, ok := manager.RealizedVolatility(inst, 24*time.Hour)
	if !ok {
		t.Fatal("expected realized volatility to be available")
	}
	if vol <= 0 {
		t.Fatalf("expected positive realized volatility, got %.2f", vol)
	}
}

func TestBestEffortQuoteUsesLiveMarketData(t *testing.T) {
	manager := NewManager(stubHistoricalClient{
		md: &Snapshot{
			ObservedAt: time.Now().UTC(),
			Last:       211.4,
			Bid:        211.3,
			Ask:        211.5,
			Volume:     880000,
		},
	}, nil, time.Minute)

	inst := model.Instrument{Symbol: "IWM", SecType: "STK", Currency: "USD", Exchange: "SMART"}
	resolved, quote, ok := manager.BestEffortQuote(context.Background(), inst)
	if !ok {
		t.Fatal("expected best-effort quote to succeed")
	}
	if resolved.Symbol != "IWM" {
		t.Fatalf("expected resolved symbol IWM, got %s", resolved.Symbol)
	}
	if quote.Last != 211.4 || quote.Bid != 211.3 || quote.Ask != 211.5 {
		t.Fatalf("unexpected quote %+v", quote)
	}

	cached, ok := manager.LatestQuote(inst)
	if !ok {
		t.Fatal("expected best-effort quote to populate cache")
	}
	if cached.Volume != 880000 {
		t.Fatalf("expected cached volume 880000, got %d", cached.Volume)
	}
}

func TestFreshnessReportCountsFreshStaleAndMissingQuotes(t *testing.T) {
	manager := NewManager(nil, nil, time.Minute)
	now := time.Now().UTC()

	fresh := model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"}
	stale := model.Instrument{Symbol: "QQQ", SecType: "STK", Currency: "USD", Exchange: "SMART"}
	missing := model.Instrument{Symbol: "IWM", SecType: "STK", Currency: "USD", Exchange: "SMART"}

	manager.UpsertQuote(fresh, model.MarketQuote{
		ObservedAt: now.Add(-30 * time.Second),
		Last:       505.2,
		Bid:        505.1,
		Ask:        505.3,
	})
	manager.UpsertQuote(stale, model.MarketQuote{
		ObservedAt: now.Add(-5 * time.Minute),
		Last:       438.7,
		Bid:        438.6,
		Ask:        438.8,
	})

	report := manager.FreshnessReport([]model.Instrument{fresh, stale, missing}, now, 2*time.Minute)
	if report.Total != 3 {
		t.Fatalf("expected total 3, got %d", report.Total)
	}
	if report.Fresh != 1 {
		t.Fatalf("expected 1 fresh quote, got %d", report.Fresh)
	}
	if report.Stale != 1 {
		t.Fatalf("expected 1 stale quote, got %d", report.Stale)
	}
	if report.Missing != 1 {
		t.Fatalf("expected 1 missing quote, got %d", report.Missing)
	}
	if len(report.MissingSymbols) != 1 || report.MissingSymbols[0] != "IWM" {
		t.Fatalf("expected missing symbol IWM, got %+v", report.MissingSymbols)
	}
	if len(report.StaleSymbols) != 1 || report.StaleSymbols[0] != "QQQ" {
		t.Fatalf("expected stale symbol QQQ, got %+v", report.StaleSymbols)
	}
	if report.OldestAge < 5*time.Minute-time.Second {
		t.Fatalf("expected oldest age near 5m, got %s", report.OldestAge)
	}
	if report.NewestAge > time.Minute {
		t.Fatalf("expected newest age under 1m, got %s", report.NewestAge)
	}
}

func TestManagerSnapshotUsesCachedQuote(t *testing.T) {
	manager := NewManager(nil, nil, time.Minute)
	inst := model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"}
	now := time.Now().UTC()

	manager.UpsertQuote(inst, model.MarketQuote{
		ObservedAt: now,
		Last:       501.2,
		Bid:        501.1,
		Ask:        501.3,
		Volume:     123456,
	})

	snapshot, err := manager.Snapshot(context.Background(), inst)
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshot.Last != 501.2 || snapshot.Bid != 501.1 || snapshot.Ask != 501.3 {
		t.Fatalf("unexpected snapshot %+v", snapshot)
	}
	if snapshot.Symbol != "SPY" {
		t.Fatalf("expected symbol SPY, got %s", snapshot.Symbol)
	}
}
