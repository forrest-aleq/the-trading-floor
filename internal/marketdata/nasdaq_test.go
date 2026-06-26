package marketdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestNasdaqProviderSnapshotParsesPrimaryQuote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/quote/MU/info" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("assetclass"); got != "stocks" {
			t.Fatalf("assetclass = %q, want stocks", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"symbol":"MU","primaryData":{"lastSalePrice":"$1,157.25","bidPrice":"$1,157.13","askPrice":"$1,158.00","volume":"1,963,864.243649","lastTradeTimestamp":"Jun 26, 2026 7:37 AM ET"},"secondaryData":{}}}`))
	}))
	defer server.Close()

	provider := NewNasdaqProvider()
	provider.baseURL = server.URL
	provider.client = server.Client()

	snapshot, err := provider.Snapshot(context.Background(), model.Instrument{Symbol: "MU", SecType: "STK"})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshot.Last != 1157.25 || snapshot.Bid != 1157.13 || snapshot.Ask != 1158 {
		t.Fatalf("unexpected snapshot %+v", snapshot)
	}
	if snapshot.Volume != 1963864 {
		t.Fatalf("volume = %d, want 1963864", snapshot.Volume)
	}
	if snapshot.ObservedAt.IsZero() {
		t.Fatal("expected observed_at")
	}
}

func TestNasdaqProviderRoutesKnownETFAssetClass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("assetclass"); got != "etf" {
			t.Fatalf("assetclass = %q, want etf", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"symbol":"SPY","primaryData":{"lastSalePrice":"$729.63","bidPrice":"$729.54","askPrice":"$729.63","volume":"300","lastTradeTimestamp":"Jun 26, 2026 7:37 AM ET"},"secondaryData":{}}}`))
	}))
	defer server.Close()

	provider := NewNasdaqProvider()
	provider.baseURL = server.URL
	provider.client = server.Client()

	if _, err := provider.Snapshot(context.Background(), model.Instrument{Symbol: "SPY", SecType: "STK"}); err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
}

func TestParseNasdaqSnapshotUsesSecondaryWhenPrimaryEmpty(t *testing.T) {
	snapshot, err := parseNasdaqSnapshot([]byte(`{"data":{"symbol":"SNY","primaryData":{"lastSalePrice":"","bidPrice":"","askPrice":""},"secondaryData":{"lastSalePrice":"$42.1499","volume":"10","lastTradeTimestamp":"Closed at Jun 25, 2026 4:00 PM ET"}}}`), model.Instrument{Symbol: "SNY"})
	if err != nil {
		t.Fatalf("parseNasdaqSnapshot failed: %v", err)
	}
	if snapshot.Last != 42.1499 {
		t.Fatalf("last = %.4f, want 42.1499", snapshot.Last)
	}
}
