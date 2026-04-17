package marketdata

import (
	"context"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

// Snapshot is the provider-neutral market-state snapshot used by the local bus.
type Snapshot struct {
	ConID      int64
	Symbol     string
	Last       float64
	Bid        float64
	Ask        float64
	Volume     int64
	ObservedAt time.Time
}

// HistoricalBar is the provider-neutral historical bar shape used for cached
// price fallback and realized-volatility bootstrap.
type HistoricalBar struct {
	Time  time.Time
	Open  float64
	High  float64
	Low   float64
	Close float64
}

// SnapshotProvider returns current market-state snapshots. Providers should be
// external market data services or local cache feeders, not the broker by
// default.
type SnapshotProvider interface {
	Snapshot(context.Context, model.Instrument) (*Snapshot, error)
}

// HistoricalProvider returns historical bars for bootstrapping and fallback.
type HistoricalProvider interface {
	HistoricalBars(context.Context, model.Instrument, time.Time, string, string, string, bool) ([]HistoricalBar, error)
}

// RequestBudget is an optional provider-neutral rate limiter.
type RequestBudget interface {
	Acquire(context.Context) error
}

// LegacyIBKRSnapshotClient is the minimum IBKR interface needed to adapt the
// broker into the legacy market-state path.
type LegacyIBKRSnapshotClient interface {
	ReqMarketData(context.Context, model.Instrument) (*ibkr.MarketData, error)
}

// LegacyIBKRHistoricalClient is the minimum IBKR historical-bar interface
// needed to adapt the broker into the legacy market-state path.
type LegacyIBKRHistoricalClient interface {
	HistoricalBars(context.Context, model.Instrument, time.Time, string, string, string, bool) ([]ibkr.HistoricalBar, error)
}

// LegacyIBKRProvider adapts the broker into the provider-neutral market-state
// interface. This exists only as an explicit compatibility path and should not
// be the default runtime choice.
type LegacyIBKRProvider struct {
	snapshots LegacyIBKRSnapshotClient
	history   LegacyIBKRHistoricalClient
}

func NewLegacyIBKRProvider(snapshotClient LegacyIBKRSnapshotClient, historicalClient LegacyIBKRHistoricalClient) *LegacyIBKRProvider {
	if snapshotClient == nil && historicalClient == nil {
		return nil
	}
	return &LegacyIBKRProvider{
		snapshots: snapshotClient,
		history:   historicalClient,
	}
}

func (p *LegacyIBKRProvider) Snapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	if p == nil || p.snapshots == nil {
		return nil, nil
	}
	md, err := p.snapshots.ReqMarketData(ctx, inst)
	if err != nil || md == nil {
		return nil, err
	}
	observedAt := time.Now().UTC()
	if md.Timestamp > 0 {
		observedAt = time.UnixMilli(md.Timestamp).UTC()
	}
	return &Snapshot{
		ConID:      md.ConID,
		Symbol:     md.Symbol,
		Last:       md.Last,
		Bid:        md.Bid,
		Ask:        md.Ask,
		Volume:     md.Volume,
		ObservedAt: observedAt,
	}, nil
}

func (p *LegacyIBKRProvider) HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]HistoricalBar, error) {
	if p == nil || p.history == nil {
		return nil, nil
	}
	bars, err := p.history.HistoricalBars(ctx, inst, end, duration, barSize, whatToShow, useRTH)
	if err != nil {
		return nil, err
	}
	out := make([]HistoricalBar, 0, len(bars))
	for _, bar := range bars {
		out = append(out, HistoricalBar{
			Time:  bar.Time,
			Open:  bar.Open,
			High:  bar.High,
			Low:   bar.Low,
			Close: bar.Close,
		})
	}
	return out, nil
}
