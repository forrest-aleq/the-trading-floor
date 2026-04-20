package marketdata

import (
	"context"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestHistoricalSnapshotProviderUsesLatestCloseAsFreshSnapshot(t *testing.T) {
	provider := NewHistoricalSnapshotProvider(stubHistoricalClient{
		bars: []HistoricalBar{
			{Time: time.Now().Add(-2 * time.Hour), Close: 98.5, Volume: 100},
			{Time: time.Now().Add(-time.Hour), Close: 101.25, Volume: 200},
		},
	})

	before := time.Now().UTC()
	snapshot, err := provider.Snapshot(context.Background(), model.Instrument{Symbol: "SPY", SecType: "STK"})
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshot.Last != 101.25 {
		t.Fatalf("expected latest close 101.25, got %.2f", snapshot.Last)
	}
	if snapshot.Volume != 200 {
		t.Fatalf("expected latest volume 200, got %d", snapshot.Volume)
	}
	if snapshot.ObservedAt.Before(before) || snapshot.ObservedAt.After(after.Add(time.Second)) {
		t.Fatalf("expected fresh observation timestamp, got %s", snapshot.ObservedAt)
	}
}

func TestHistoricalSnapshotProviderBatchReturnsPartialSuccess(t *testing.T) {
	provider := NewHistoricalSnapshotProvider(stubHistoricalClient{
		bars: []HistoricalBar{{Time: time.Now().UTC(), Close: 42}},
	})
	provider.minRefreshInterval = 0

	snapshots, err := provider.Snapshots(context.Background(), []model.Instrument{
		{Symbol: "SPY", SecType: "STK", Currency: "USD"},
		{Symbol: "QQQ", SecType: "STK", Currency: "USD"},
	})
	if err != nil {
		t.Fatalf("Snapshots failed: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("expected two snapshots, got %d", len(snapshots))
	}
}

func TestPolygonAggsGranularityParsesIntradayBars(t *testing.T) {
	multiplier, timespan := polygonAggsGranularity("1 hour")
	if multiplier != "1" || timespan != "hour" {
		t.Fatalf("expected 1/hour, got %s/%s", multiplier, timespan)
	}

	multiplier, timespan = polygonAggsGranularity("5 mins")
	if multiplier != "5" || timespan != "minute" {
		t.Fatalf("expected 5/minute, got %s/%s", multiplier, timespan)
	}
}
