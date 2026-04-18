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

func TestPolygonProviderStockSnapshotAndHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, polygonStocksSnapshotPath+"/SPY"):
			if got := r.URL.Query().Get("apiKey"); got != "test-key" {
				t.Fatalf("stock snapshot apiKey = %q, want test-key", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"ticker": {
					"ticker": "SPY",
					"updated": 1713355200000,
					"lastTrade": {"p": 510.25, "t": 1713355200000000000},
					"lastQuote": {"p": 510.20, "P": 510.30, "t": 1713355200000000000},
					"day": {"c": 509.8, "v": 1234567}
				}
			}`))
		case strings.Contains(r.URL.Path, "/v2/aggs/ticker/SPY/range/1/day/"):
			if got := r.URL.Query().Get("apiKey"); got != "test-key" {
				t.Fatalf("history apiKey = %q, want test-key", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"results": [
					{"t": 1713225600000, "o": 507.0, "h": 509.0, "l": 506.5, "c": 508.4, "v": 100},
					{"t": 1713312000000, "o": 508.6, "h": 511.1, "l": 508.2, "c": 510.2, "v": 110}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewPolygonProvider("test-key")
	if err != nil {
		t.Fatalf("NewPolygonProvider failed: %v", err)
	}
	provider.baseURL = server.URL
	provider.client = server.Client()

	inst := model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD"}

	snapshot, err := provider.Snapshot(context.Background(), inst)
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshot.Symbol != "SPY" || snapshot.Last != 510.25 || snapshot.Bid != 510.20 || snapshot.Ask != 510.30 {
		t.Fatalf("unexpected snapshot %+v", snapshot)
	}
	if snapshot.Volume != 1234567 {
		t.Fatalf("snapshot volume = %d, want 1234567", snapshot.Volume)
	}
	if snapshot.ObservedAt.IsZero() {
		t.Fatal("expected observed time to be set")
	}

	bars, err := provider.HistoricalBars(context.Background(), inst, time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC), "2 D", "1 day", "", true)
	if err != nil {
		t.Fatalf("HistoricalBars failed: %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("historical bars len = %d, want 2", len(bars))
	}
	if bars[1].Close != 510.2 {
		t.Fatalf("latest close = %.2f, want 510.2", bars[1].Close)
	}
}

func TestPolygonProviderIndexSnapshotUsesIndexEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != polygonIndicesSnapshotPath {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("ticker"); got != "I:VIX" {
			t.Fatalf("ticker = %q, want I:VIX", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{
					"ticker": "I:VIX",
					"value": 19.42,
					"last_updated": 1713355200000000000,
					"session": {"close": 19.40, "volume": 0}
				}
			]
		}`))
	}))
	defer server.Close()

	provider, err := NewPolygonProvider("test-key")
	if err != nil {
		t.Fatalf("NewPolygonProvider failed: %v", err)
	}
	provider.baseURL = server.URL
	provider.client = server.Client()

	snapshot, err := provider.Snapshot(context.Background(), model.Instrument{Symbol: "VIX", SecType: "IND", Currency: "USD"})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshot.Symbol != "VIX" || snapshot.Last != 19.42 {
		t.Fatalf("unexpected snapshot %+v", snapshot)
	}
}

func TestResolvePolygonAPIKeySupportsMassiveAlias(t *testing.T) {
	t.Setenv("POLYGON_API_KEY", "")
	t.Setenv("MASSIVE_API_KEY", "massive-key")

	if got := resolvePolygonAPIKey(""); got != "massive-key" {
		t.Fatalf("resolvePolygonAPIKey() = %q, want massive-key", got)
	}
}
