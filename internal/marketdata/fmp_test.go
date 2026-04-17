package marketdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestFMPSymbolPrefixesIndices(t *testing.T) {
	if got := fmpSymbol(model.Instrument{Symbol: "VIX", SecType: "IND"}); got != "^VIX" {
		t.Fatalf("fmpSymbol() = %q, want ^VIX", got)
	}
	if got := fmpSymbol(model.Instrument{Symbol: "SPY", SecType: "STK"}); got != "SPY" {
		t.Fatalf("fmpSymbol() = %q, want SPY", got)
	}
}

func TestFMPProviderSnapshotAndHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case defaultFMPQuotePath:
			if got := r.URL.Query().Get("symbol"); got != "^VIX" {
				t.Fatalf("quote symbol = %q, want ^VIX", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"symbol":"^VIX","price":19.42,"bid":19.3,"ask":19.5,"volume":123456,"lastSaleTime":1713355200000}]`))
		case defaultFMPHistory:
			if got := r.URL.Query().Get("symbol"); got != "^VIX" {
				t.Fatalf("history symbol = %q, want ^VIX", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"historical":[{"date":"2026-04-15","open":18.1,"high":19.0,"low":17.9,"close":18.6},{"date":"2026-04-16","open":18.7,"high":19.8,"low":18.5,"close":19.4}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewFMPProvider("test-key")
	if err != nil {
		t.Fatalf("NewFMPProvider failed: %v", err)
	}
	provider.baseURL = server.URL
	provider.client = server.Client()

	inst := model.Instrument{Symbol: "VIX", SecType: "IND", Currency: "USD"}

	snapshot, err := provider.Snapshot(context.Background(), inst)
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshot.Last != 19.42 || snapshot.Bid != 19.3 || snapshot.Ask != 19.5 {
		t.Fatalf("unexpected snapshot %+v", snapshot)
	}
	if snapshot.Volume != 123456 {
		t.Fatalf("snapshot volume = %d, want 123456", snapshot.Volume)
	}
	if snapshot.ObservedAt.IsZero() {
		t.Fatal("expected observed time to be set")
	}

	bars, err := provider.HistoricalBars(context.Background(), inst, time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC), "2 D", "1 hour", "", true)
	if err != nil {
		t.Fatalf("HistoricalBars failed: %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("historical bars len = %d, want 2", len(bars))
	}
	if bars[1].Close != 19.4 {
		t.Fatalf("latest close = %.2f, want 19.4", bars[1].Close)
	}
}

func TestParseFMPQuoteSupportsWrappedShapes(t *testing.T) {
	quote, err := parseFMPQuote([]byte(`{"data":[{"symbol":"SPY","price":510.2,"bid":510.1,"ask":510.3,"volume":123}]}`))
	if err != nil {
		t.Fatalf("parseFMPQuote failed: %v", err)
	}
	if quote.Symbol != "SPY" || quote.Price != 510.2 {
		t.Fatalf("unexpected parsed quote %+v", quote)
	}
}

func TestParseFMPHistoricalBarsRejectsUnknownShape(t *testing.T) {
	_, err := parseFMPHistoricalBars([]byte(`{"unexpected":true}`))
	if err == nil || !strings.Contains(err.Error(), "unexpected FMP historical response shape") {
		t.Fatalf("unexpected error %v", err)
	}
}
