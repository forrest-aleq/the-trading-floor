package marketdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

type stubHistoricalClient struct {
	marketErr error
	bars      []ibkr.HistoricalBar
}

func (s stubHistoricalClient) ReqMarketData(context.Context, model.Instrument) (*ibkr.MarketData, error) {
	if s.marketErr != nil {
		return nil, s.marketErr
	}
	return &ibkr.MarketData{}, nil
}

func (s stubHistoricalClient) HistoricalBars(context.Context, model.Instrument, time.Time, string, string, string, bool) ([]ibkr.HistoricalBar, error) {
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
		bars: []ibkr.HistoricalBar{
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
