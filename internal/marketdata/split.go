package marketdata

import (
	"context"
	"fmt"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

// SplitProvider composes a live snapshot source and a historical-bar source.
// This is useful when the runtime has one provider tier for current quotes and
// another provider tier for bootstrap/history.
type SplitProvider struct {
	snapshots SnapshotProvider
	history   HistoricalProvider
}

func NewSplitProvider(snapshots SnapshotProvider, history HistoricalProvider) *SplitProvider {
	if snapshots == nil && history == nil {
		return nil
	}
	return &SplitProvider{
		snapshots: snapshots,
		history:   history,
	}
}

func (p *SplitProvider) Snapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	if p == nil || p.snapshots == nil {
		return nil, fmt.Errorf("split provider missing snapshot source")
	}
	return p.snapshots.Snapshot(ctx, inst)
}

func (p *SplitProvider) Snapshots(ctx context.Context, instruments []model.Instrument) (map[string]*Snapshot, error) {
	if p == nil || p.snapshots == nil {
		return nil, fmt.Errorf("split provider missing snapshot source")
	}
	batch, ok := p.snapshots.(BatchSnapshotProvider)
	if !ok {
		return nil, fmt.Errorf("split provider snapshot source does not support batched snapshots")
	}
	return batch.Snapshots(ctx, instruments)
}

func (p *SplitProvider) HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]HistoricalBar, error) {
	if p == nil || p.history == nil {
		return nil, fmt.Errorf("split provider missing historical source")
	}
	return p.history.HistoricalBars(ctx, inst, end, duration, barSize, whatToShow, useRTH)
}
