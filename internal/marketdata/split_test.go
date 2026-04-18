package marketdata

import (
	"context"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestSplitProviderRoutesSnapshotAndHistorySeparately(t *testing.T) {
	provider := NewSplitProvider(
		fallbackSnapshotStub{snapshot: &Snapshot{Last: 111, ObservedAt: time.Now().UTC()}},
		fallbackHistoryStub{bars: []HistoricalBar{{Time: time.Now().UTC(), Close: 222}}},
	)

	snapshot, err := provider.Snapshot(context.Background(), model.Instrument{Symbol: "SPY"})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshot == nil || snapshot.Last != 111 {
		t.Fatalf("unexpected snapshot %+v", snapshot)
	}

	bars, err := provider.HistoricalBars(context.Background(), model.Instrument{Symbol: "SPY"}, time.Now().UTC(), "1 D", "1 day", "", true)
	if err != nil {
		t.Fatalf("HistoricalBars failed: %v", err)
	}
	if len(bars) != 1 || bars[0].Close != 222 {
		t.Fatalf("unexpected bars %+v", bars)
	}
}
