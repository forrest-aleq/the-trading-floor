package feeds

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestResolveFMPAPIKeyPrecedence(t *testing.T) {
	t.Setenv("FMP_API_KEY", "from-fmp")
	t.Setenv("EARNINGS_API_KEY", "from-legacy")

	if got := resolveFMPAPIKey("explicit"); got != "explicit" {
		t.Fatalf("explicit key = %q, want explicit", got)
	}
	if got := resolveFMPAPIKey(""); got != "from-fmp" {
		t.Fatalf("resolved key = %q, want from-fmp", got)
	}
}

func TestResolveFMPAPIKeyFallsBackToLegacyAlias(t *testing.T) {
	t.Setenv("FMP_API_KEY", "")
	t.Setenv("EARNINGS_API_KEY", "from-legacy")

	if got := resolveFMPAPIKey(""); got != "from-legacy" {
		t.Fatalf("resolved key = %q, want from-legacy", got)
	}
}

func TestNewEarningsFeedAllowsUnboundedUniverseWhenWatchlistEmpty(t *testing.T) {
	t.Parallel()

	feed := NewEarningsFeed("key", nil)
	if len(feed.watch) != 0 {
		t.Fatalf("watchlist len = %d, want 0", len(feed.watch))
	}
}

func TestNewEarningsFeedOnlyTracksStockSymbols(t *testing.T) {
	t.Parallel()

	feed := NewEarningsFeed("key", []model.Instrument{
		{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "ES", SecType: "FUT", Exchange: "CME", Currency: "USD"},
		{Symbol: "  ", SecType: "STK", Exchange: "SMART", Currency: "USD"},
	})
	if len(feed.watch) != 1 {
		t.Fatalf("watchlist len = %d, want 1", len(feed.watch))
	}
	if _, ok := feed.watch["AAPL"]; !ok {
		t.Fatal("expected AAPL to be tracked")
	}
}
