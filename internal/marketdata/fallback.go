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

func (p *FallbackProvider) HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]HistoricalBar, error) {
	if p == nil || p.primaryHistory == nil {
		return nil, fmt.Errorf("nil fallback historical provider")
	}

	bars, err := p.primaryHistory.HistoricalBars(ctx, inst, end, duration, barSize, whatToShow, useRTH)
	if err == nil && len(bars) > 0 {
		return bars, nil
	}
	if p.secondaryHistory == nil {
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
