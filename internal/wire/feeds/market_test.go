package feeds

import (
	"errors"
	"testing"
	"time"
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
