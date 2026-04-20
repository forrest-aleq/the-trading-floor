package marketdata

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

const (
	defaultHistoricalSnapshotDuration = "7 D"
	defaultHistoricalSnapshotBarSize  = "1 day"
	defaultHistoricalSnapshotCacheTTL = 10 * time.Minute
	defaultHistoricalSnapshotMinGap   = 15 * time.Second
	defaultHistoricalSnapshotBatchMax = 4
)

// HistoricalSnapshotProvider converts a historical aggregate source into a
// quote-like snapshot. It is intentionally used for free-tier paper runtimes
// where delayed aggregate data is acceptable but broker-backed market data is
// not.
type HistoricalSnapshotProvider struct {
	history            HistoricalProvider
	duration           string
	barSize            string
	cacheTTL           time.Duration
	minRefreshInterval time.Duration
	maxRefreshPerBatch int

	mu          sync.Mutex
	cache       map[string]*Snapshot
	nextRefresh time.Time
}

func NewHistoricalSnapshotProvider(history HistoricalProvider) *HistoricalSnapshotProvider {
	if history == nil {
		return nil
	}
	return &HistoricalSnapshotProvider{
		history:            history,
		duration:           defaultHistoricalSnapshotDuration,
		barSize:            defaultHistoricalSnapshotBarSize,
		cacheTTL:           defaultHistoricalSnapshotCacheTTL,
		minRefreshInterval: defaultHistoricalSnapshotMinGap,
		maxRefreshPerBatch: defaultHistoricalSnapshotBatchMax,
		cache:              make(map[string]*Snapshot),
	}
}

func (p *HistoricalSnapshotProvider) Snapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	if p == nil || p.history == nil {
		return nil, fmt.Errorf("nil historical snapshot provider")
	}
	key := inst.Key()
	if snapshot, ok := p.cachedSnapshot(key, time.Now().UTC()); ok {
		return snapshot, nil
	}
	return p.refreshSnapshot(ctx, inst)
}

func (p *HistoricalSnapshotProvider) Snapshots(ctx context.Context, instruments []model.Instrument) (map[string]*Snapshot, error) {
	out := make(map[string]*Snapshot, len(instruments))
	now := time.Now().UTC()
	for _, inst := range instruments {
		if snapshot, ok := p.cachedSnapshot(inst.Key(), now); ok {
			out[inst.Key()] = snapshot
		}
	}

	refreshAttempts := 0
	var lastErr error
	for _, inst := range instruments {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if _, ok := out[inst.Key()]; ok {
			continue
		}
		if refreshAttempts >= p.maxRefreshPerBatch {
			continue
		}
		refreshAttempts++
		snapshot, err := p.refreshSnapshot(ctx, inst)
		if err != nil {
			lastErr = err
			continue
		}
		if snapshot != nil {
			out[inst.Key()] = snapshot
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

func (p *HistoricalSnapshotProvider) HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]HistoricalBar, error) {
	if p == nil || p.history == nil {
		return nil, fmt.Errorf("nil historical snapshot provider")
	}
	return p.history.HistoricalBars(ctx, inst, end, duration, barSize, whatToShow, useRTH)
}

func (p *HistoricalSnapshotProvider) refreshSnapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	if err := p.waitForRefreshSlot(ctx); err != nil {
		return nil, err
	}
	bars, err := p.history.HistoricalBars(ctx, inst, time.Now().UTC(), p.duration, p.barSize, "", true)
	if err != nil {
		return nil, err
	}
	for i := len(bars) - 1; i >= 0; i-- {
		bar := bars[i]
		if bar.Close <= 0 {
			continue
		}
		snapshot := &Snapshot{
			Symbol:     strings.TrimSpace(inst.Symbol),
			Last:       bar.Close,
			Volume:     bar.Volume,
			ObservedAt: time.Now().UTC(),
		}
		p.storeSnapshot(inst.Key(), snapshot)
		return cloneSnapshot(snapshot), nil
	}
	return nil, fmt.Errorf("historical snapshot unavailable for %s", inst.Label())
}

func (p *HistoricalSnapshotProvider) cachedSnapshot(key string, now time.Time) (*Snapshot, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := p.cache[key]
	if snapshot == nil {
		return nil, false
	}
	if p.cacheTTL > 0 && now.Sub(snapshot.ObservedAt.UTC()) > p.cacheTTL {
		return nil, false
	}
	return cloneSnapshot(snapshot), true
}

func (p *HistoricalSnapshotProvider) storeSnapshot(key string, snapshot *Snapshot) {
	if snapshot == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[key] = cloneSnapshot(snapshot)
}

func (p *HistoricalSnapshotProvider) waitForRefreshSlot(ctx context.Context) error {
	for {
		now := time.Now().UTC()
		p.mu.Lock()
		wait := p.nextRefresh.Sub(now)
		if wait <= 0 {
			p.nextRefresh = now.Add(p.minRefreshInterval)
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func cloneSnapshot(snapshot *Snapshot) *Snapshot {
	if snapshot == nil {
		return nil
	}
	cp := *snapshot
	return &cp
}
