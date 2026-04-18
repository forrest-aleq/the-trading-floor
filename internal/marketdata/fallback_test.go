package marketdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

type fallbackSnapshotStub struct {
	snapshot *Snapshot
	err      error
}

func (s fallbackSnapshotStub) Snapshot(context.Context, model.Instrument) (*Snapshot, error) {
	return s.snapshot, s.err
}

type fallbackHistoryStub struct {
	bars []HistoricalBar
	err  error
}

func (s fallbackHistoryStub) Snapshot(context.Context, model.Instrument) (*Snapshot, error) {
	return &Snapshot{Last: 1, ObservedAt: time.Now().UTC()}, nil
}

func (s fallbackHistoryStub) HistoricalBars(context.Context, model.Instrument, time.Time, string, string, string, bool) ([]HistoricalBar, error) {
	return s.bars, s.err
}

func TestFallbackProviderSnapshotFallsBackOnPrimaryError(t *testing.T) {
	provider := NewFallbackProvider(
		fallbackSnapshotStub{err: errors.New("not entitled")},
		fallbackSnapshotStub{snapshot: &Snapshot{Last: 101, ObservedAt: time.Now().UTC()}},
	)

	snapshot, err := provider.Snapshot(context.Background(), model.Instrument{Symbol: "SPY"})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshot == nil || snapshot.Last != 101 {
		t.Fatalf("unexpected snapshot %+v", snapshot)
	}
}

func TestFallbackProviderHistoryFallsBackOnPrimaryError(t *testing.T) {
	provider := NewFallbackProvider(
		fallbackHistoryStub{err: errors.New("not entitled")},
		fallbackHistoryStub{bars: []HistoricalBar{{Time: time.Now().UTC(), Close: 100}}},
	)

	bars, err := provider.HistoricalBars(context.Background(), model.Instrument{Symbol: "SPY"}, time.Now().UTC(), "1 D", "1 day", "", true)
	if err != nil {
		t.Fatalf("HistoricalBars failed: %v", err)
	}
	if len(bars) != 1 || bars[0].Close != 100 {
		t.Fatalf("unexpected bars %+v", bars)
	}
}
