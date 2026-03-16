package marketdata

import (
	"errors"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

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
