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

type fallbackBatchStub struct {
	snapshots map[string]*Snapshot
	err       error
}

func (s fallbackBatchStub) Snapshot(context.Context, model.Instrument) (*Snapshot, error) {
	return nil, s.err
}

func (s fallbackBatchStub) Snapshots(context.Context, []model.Instrument) (map[string]*Snapshot, error) {
	return s.snapshots, s.err
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

func TestFallbackProviderSnapshotsFillsMissingPrimaryBatchRows(t *testing.T) {
	now := time.Now().UTC()
	spy := model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD"}
	mu := model.Instrument{Symbol: "MU", SecType: "STK", Currency: "USD"}
	provider := NewFallbackProvider(
		fallbackBatchStub{snapshots: map[string]*Snapshot{
			spy.Key(): {Symbol: "SPY", Last: 500, ObservedAt: now},
		}},
		fallbackBatchStub{snapshots: map[string]*Snapshot{
			mu.Key(): {Symbol: "MU", Last: 140, ObservedAt: now},
		}},
	)

	snapshots, err := provider.Snapshots(context.Background(), []model.Instrument{spy, mu})
	if err != nil {
		t.Fatalf("Snapshots failed: %v", err)
	}
	if snapshots[spy.Key()] == nil || snapshots[spy.Key()].Last != 500 {
		t.Fatalf("expected primary SPY snapshot, got %+v", snapshots[spy.Key()])
	}
	if snapshots[mu.Key()] == nil || snapshots[mu.Key()].Last != 140 {
		t.Fatalf("expected fallback MU snapshot, got %+v", snapshots[mu.Key()])
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

func TestFallbackProviderHistoryUsesSecondaryWhenPrimaryHasNoHistory(t *testing.T) {
	provider := NewFallbackProvider(
		fallbackSnapshotStub{snapshot: &Snapshot{Last: 99, ObservedAt: time.Now().UTC()}},
		fallbackHistoryStub{bars: []HistoricalBar{{Time: time.Now().UTC(), Close: 101}}},
	)

	bars, err := provider.HistoricalBars(context.Background(), model.Instrument{Symbol: "MU"}, time.Now().UTC(), "1 D", "1 day", "", true)
	if err != nil {
		t.Fatalf("HistoricalBars failed: %v", err)
	}
	if len(bars) != 1 || bars[0].Close != 101 {
		t.Fatalf("unexpected bars %+v", bars)
	}
}
