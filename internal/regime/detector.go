package regime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

// MarketDataFetcher fetches market data for regime detection.
type MarketDataFetcher interface {
	ReqMarketData(context.Context, model.Instrument) (*ibkr.MarketData, error)
}

// OnShift is called when a regime transition is detected.
type OnShift func(old, new model.Regime)

// Detector classifies the current market regime based on VIX, trend, and risk indicators.
// Runs on a timer and calls OnShift when regime changes.
type Detector struct {
	log      *slog.Logger
	client   MarketDataFetcher
	onShift  OnShift
	interval time.Duration

	mu      sync.RWMutex
	current model.Regime
}

func NewDetector(client MarketDataFetcher, onShift OnShift) *Detector {
	return &Detector{
		log:      slog.Default().With("component", "regime"),
		client:   client,
		onShift:  onShift,
		interval: 5 * time.Minute,
		current: model.Regime{
			Volatility: "medium",
			Trend:      "neutral",
			Risk:       "neutral",
			Liquidity:  "normal",
		},
	}
}

// Current returns the current regime.
func (d *Detector) Current() model.Regime {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.current
}

// Run starts the regime detection loop.
func (d *Detector) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	d.log.Info("regime detector started", "interval", d.interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.detect(ctx)
		}
	}
}

func (d *Detector) detect(ctx context.Context) {
	// Fetch VIX for volatility regime
	vixInst := model.Instrument{Symbol: "VIX", SecType: "IND", Exchange: "CBOE", Currency: "USD"}
	vixData, err := d.client.ReqMarketData(ctx, vixInst)
	if err != nil {
		d.log.Warn("regime: VIX fetch failed", "error", err)
		return
	}

	vixLevel := vixData.Last
	if vixLevel <= 0 {
		vixLevel = (vixData.Bid + vixData.Ask) / 2
	}
	if vixLevel <= 0 {
		return
	}

	// Fetch SPY for trend detection
	spyInst := model.Instrument{Symbol: "SPY", SecType: "STK", Exchange: "SMART", Currency: "USD"}
	spyData, err := d.client.ReqMarketData(ctx, spyInst)
	if err != nil {
		d.log.Warn("regime: SPY fetch failed", "error", err)
		return
	}

	// Fetch TLT for risk regime (bond proxy)
	tltInst := model.Instrument{Symbol: "TLT", SecType: "STK", Exchange: "SMART", Currency: "USD"}
	tltData, err := d.client.ReqMarketData(ctx, tltInst)
	if err != nil {
		d.log.Warn("regime: TLT fetch failed", "error", err)
		// Non-fatal, continue with partial data
		tltData = &ibkr.MarketData{}
	}

	newRegime := classify(vixLevel, spyData, tltData)

	d.mu.Lock()
	old := d.current
	changed := old.Volatility != newRegime.Volatility ||
		old.Trend != newRegime.Trend ||
		old.Risk != newRegime.Risk ||
		old.Liquidity != newRegime.Liquidity
	d.current = newRegime
	d.mu.Unlock()

	if changed {
		d.log.Warn("REGIME SHIFT detected",
			"old", old.Key(),
			"new", newRegime.Key(),
			"vix", vixLevel,
		)
		if d.onShift != nil {
			d.onShift(old, newRegime)
		}
	}
}

func classify(vix float64, spy, tlt *ibkr.MarketData) model.Regime {
	r := model.Regime{
		Liquidity: "normal",
	}

	// Volatility regime (VIX thresholds from DESIGN.md)
	switch {
	case vix >= 35:
		r.Volatility = "extreme"
	case vix >= 25:
		r.Volatility = "high"
	case vix >= 15:
		r.Volatility = "medium"
	default:
		r.Volatility = "low"
	}

	// Trend regime — simplified: if SPY bid-ask spread is wide, stressed
	// In production this would use ADX / Hurst exponent over historical data.
	// For now, we use VIX as a proxy: high VIX = likely trending down.
	switch {
	case vix >= 30:
		r.Trend = "trending_down"
	case vix <= 14:
		r.Trend = "trending_up"
	default:
		r.Trend = "neutral"
	}

	// Risk regime — TLT rising = flight to safety = risk off
	// Simplified: high VIX + TLT moving = risk_off
	if vix >= 25 {
		r.Risk = "risk_off"
	} else if vix <= 15 {
		r.Risk = "risk_on"
	} else {
		r.Risk = "neutral"
	}

	// Liquidity — wide bid-ask on SPY = stressed
	if spy != nil && spy.Bid > 0 && spy.Ask > 0 {
		spread := (spy.Ask - spy.Bid) / spy.Bid * 100
		switch {
		case spread > 0.5:
			r.Liquidity = "crisis"
		case spread > 0.1:
			r.Liquidity = "stressed"
		default:
			r.Liquidity = "normal"
		}
	}

	return r
}
