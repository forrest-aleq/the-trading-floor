package marketdata

import (
	"context"
	"fmt"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

// FallbackProvider tries a primary market data source first, then falls back
// to a secondary source when the primary errors or returns no usable data.
type FallbackProvider struct {
	primary          SnapshotProvider
	secondary        SnapshotProvider
	primaryHistory   HistoricalProvider
	secondaryHistory HistoricalProvider
}

func NewFallbackProvider(primary SnapshotProvider, secondary SnapshotProvider) *FallbackProvider {
	if primary == nil {
		return nil
	}
	provider := &FallbackProvider{
		primary:   primary,
		secondary: secondary,
	}
	if history, ok := primary.(HistoricalProvider); ok {
		provider.primaryHistory = history
	}
	if history, ok := secondary.(HistoricalProvider); ok {
		provider.secondaryHistory = history
	}
	return provider
}

func (p *FallbackProvider) Snapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	if p == nil || p.primary == nil {
		return nil, fmt.Errorf("nil fallback provider")
	}

	snapshot, err := p.primary.Snapshot(ctx, inst)
	if err == nil && snapshot != nil && !snapshot.ObservedAt.IsZero() {
		return snapshot, nil
	}
	if err == nil && snapshot != nil && (snapshot.Last > 0 || snapshot.Bid > 0 || snapshot.Ask > 0) {
		return snapshot, nil
	}
	if p.secondary == nil {
		return snapshot, err
	}

	fallbackSnapshot, fallbackErr := p.secondary.Snapshot(ctx, inst)
	if fallbackErr == nil && fallbackSnapshot != nil {
		return fallbackSnapshot, nil
	}
	if err != nil {
		return nil, err
	}
	return fallbackSnapshot, fallbackErr
}

func (p *FallbackProvider) Snapshots(ctx context.Context, instruments []model.Instrument) (map[string]*Snapshot, error) {
	if p == nil || p.primary == nil {
		return nil, fmt.Errorf("nil fallback provider")
	}
	if len(instruments) == 0 {
		return map[string]*Snapshot{}, nil
	}

	out, primaryErr := snapshotsFromProvider(ctx, p.primary, instruments)
	missing := missingSnapshots(instruments, out)
	if len(missing) == 0 {
		return out, nil
	}
	if p.secondary == nil {
		if len(out) > 0 {
			return out, nil
		}
		return out, primaryErr
	}

	fallbackOut, fallbackErr := snapshotsFromProvider(ctx, p.secondary, missing)
	for key, snapshot := range fallbackOut {
		if usableSnapshot(snapshot) {
			out[key] = snapshot
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	if primaryErr != nil {
		return out, primaryErr
	}
	return out, fallbackErr
}

func (p *FallbackProvider) HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]HistoricalBar, error) {
	if p == nil {
		return nil, fmt.Errorf("nil fallback historical provider")
	}

	var bars []HistoricalBar
	var err error
	if p.primaryHistory != nil {
		bars, err = p.primaryHistory.HistoricalBars(ctx, inst, end, duration, barSize, whatToShow, useRTH)
		if err == nil && len(bars) > 0 {
			return bars, nil
		}
	}
	if p.secondaryHistory == nil {
		if err == nil {
			err = fmt.Errorf("fallback historical provider has no historical source")
		}
		return bars, err
	}

	fallbackBars, fallbackErr := p.secondaryHistory.HistoricalBars(ctx, inst, end, duration, barSize, whatToShow, useRTH)
	if fallbackErr == nil && len(fallbackBars) > 0 {
		return fallbackBars, nil
	}
	if err != nil {
		return nil, err
	}
	return fallbackBars, fallbackErr
}

func snapshotsFromProvider(ctx context.Context, provider SnapshotProvider, instruments []model.Instrument) (map[string]*Snapshot, error) {
	if provider == nil {
		return map[string]*Snapshot{}, fmt.Errorf("nil snapshot provider")
	}
	if batch, ok := provider.(BatchSnapshotProvider); ok {
		snapshots, err := batch.Snapshots(ctx, instruments)
		if snapshots == nil {
			snapshots = map[string]*Snapshot{}
		}
		return snapshots, err
	}

	out := make(map[string]*Snapshot, len(instruments))
	var lastErr error
	for _, inst := range instruments {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		snapshot, err := provider.Snapshot(ctx, inst)
		if err != nil {
			lastErr = err
			continue
		}
		if usableSnapshot(snapshot) {
			out[inst.Key()] = snapshot
		}
	}
	if len(out) == 0 && lastErr != nil {
		return out, lastErr
	}
	return out, nil
}

func missingSnapshots(instruments []model.Instrument, snapshots map[string]*Snapshot) []model.Instrument {
	missing := make([]model.Instrument, 0)
	for _, inst := range instruments {
		if !usableSnapshot(snapshots[inst.Key()]) {
			missing = append(missing, inst)
		}
	}
	return missing
}

func usableSnapshot(snapshot *Snapshot) bool {
	return snapshot != nil && !snapshot.ObservedAt.IsZero() && bestPrice(snapshot) > 0
}
