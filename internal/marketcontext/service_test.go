package marketcontext

import (
	"context"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type stubPriceView struct {
	price  float64
	change map[time.Duration]float64
}

func (s stubPriceView) LatestPrice(model.Instrument) (float64, bool) {
	return s.price, s.price > 0
}

func (s stubPriceView) PriceChange(_ model.Instrument, window time.Duration) (float64, bool) {
	value, ok := s.change[window]
	return value, ok
}

func (s stubPriceView) BestEffortPrice(_ context.Context, inst model.Instrument) (model.Instrument, float64, bool) {
	if s.price <= 0 {
		return model.Instrument{}, 0, false
	}
	resolved := inst
	if resolved.SecType == "STK" && inst.Symbol == "VIX" {
		resolved.SecType = "IND"
		resolved.Exchange = "CBOE"
	}
	return resolved, s.price, true
}

type blockingPriceView struct{}

func (blockingPriceView) LatestPrice(model.Instrument) (float64, bool) { return 0, false }

func (blockingPriceView) PriceChange(model.Instrument, time.Duration) (float64, bool) {
	return 0, false
}

func (blockingPriceView) BestEffortPrice(ctx context.Context, _ model.Instrument) (model.Instrument, float64, bool) {
	<-ctx.Done()
	return model.Instrument{}, 0, false
}

func TestBuildOpportunityContextIncludesConsensusAndPricePath(t *testing.T) {
	service := NewService(stubPriceView{
		price: 101.25,
		change: map[time.Duration]float64{
			15 * time.Minute: 1.2,
			time.Hour:        2.4,
			4 * time.Hour:    3.1,
		},
	})

	opp := &model.Opportunity{
		Instruments: []model.Instrument{{
			Symbol:   "NVDA",
			SecType:  "STK",
			Currency: "USD",
		}},
	}
	sig := signal.Signal{
		Timestamp:  time.Now().Add(-30 * time.Minute),
		Raw:        []byte(`{"symbol":"NVDA","eps":1.20,"epsEstimated":1.00,"revenue":1200,"revenueEstimated":1000}`),
		Translated: "earnings beat",
	}

	ctx := service.BuildOpportunityContext(opp, sig)
	if ctx == nil {
		t.Fatal("expected market context")
	}
	if !ctx.ConsensusAvailable {
		t.Fatal("expected consensus data to be extracted")
	}
	if ctx.CurrentPrice != 101.25 {
		t.Fatalf("unexpected current price %.2f", ctx.CurrentPrice)
	}
	if ctx.Return1hPct != 2.4 {
		t.Fatalf("unexpected 1h return %.2f", ctx.Return1hPct)
	}
	if ctx.SurpriseMagnitude <= 0 {
		t.Fatalf("expected positive surprise magnitude, got %.2f", ctx.SurpriseMagnitude)
	}
}

func TestBuildThesisContextRehydratesPriceFromResolvedInstrument(t *testing.T) {
	service := NewService(stubPriceView{
		price: 18.4,
		change: map[time.Duration]float64{
			15 * time.Minute: 0.8,
			time.Hour:        1.1,
		},
	})

	thesis := &model.Thesis{
		Instrument: model.Instrument{
			Symbol:   "VIX",
			SecType:  "STK",
			Currency: "USD",
			Exchange: "SMART",
		},
		MarketContext: &model.MarketContext{
			ConsensusAvailable: true,
			ActualEPS:          1.2,
			EstimatedEPS:       1.0,
		},
	}

	ctx := service.BuildThesisContext(context.Background(), thesis)
	if ctx == nil {
		t.Fatal("expected thesis context")
	}
	if ctx.CurrentPrice != 18.4 {
		t.Fatalf("expected current price 18.4, got %.2f", ctx.CurrentPrice)
	}
	if ctx.Instrument.SecType != "IND" {
		t.Fatalf("expected resolved sec type IND, got %q", ctx.Instrument.SecType)
	}
	if !ctx.ConsensusAvailable {
		t.Fatal("expected prior consensus fields to be preserved")
	}
}

func TestBuildThesisContextRespectsCallerDeadline(t *testing.T) {
	service := NewService(blockingPriceView{})
	thesis := &model.Thesis{
		Instrument: model.Instrument{
			Symbol:   "SPY",
			SecType:  "STK",
			Currency: "USD",
			Exchange: "SMART",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	enriched := service.BuildThesisContext(ctx, thesis)
	if enriched == nil {
		t.Fatal("expected thesis context")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("expected market context build to respect deadline, took %s", elapsed)
	}
	if enriched.CurrentPrice != 0 {
		t.Fatalf("expected no hydrated price, got %.2f", enriched.CurrentPrice)
	}
}
