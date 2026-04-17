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
	quote  model.MarketQuote
	vol    map[time.Duration]float64
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

func (s stubPriceView) LatestQuote(model.Instrument) (model.MarketQuote, bool) {
	return s.quote, s.quote.ReferencePrice() > 0
}

func (s stubPriceView) RealizedVolatility(_ model.Instrument, window time.Duration) (float64, bool) {
	value, ok := s.vol[window]
	return value, ok
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
		quote: model.MarketQuote{
			ObservedAt: time.Now().Add(-5 * time.Second),
			Last:       101.25,
			Bid:        101.2,
			Ask:        101.3,
			Volume:     1800000,
		},
		vol: map[time.Duration]float64{
			24 * time.Hour:     32.5,
			5 * 24 * time.Hour: 28.1,
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
	if ctx.BidPrice != 101.2 || ctx.AskPrice != 101.3 {
		t.Fatalf("unexpected quote %.2f / %.2f", ctx.BidPrice, ctx.AskPrice)
	}
	if ctx.SpreadBps <= 0 {
		t.Fatalf("expected spread bps to be populated, got %.2f", ctx.SpreadBps)
	}
	if ctx.LastVolume != 1800000 {
		t.Fatalf("expected last volume 1800000, got %d", ctx.LastVolume)
	}
	if ctx.RealizedVol1dPct != 32.5 {
		t.Fatalf("unexpected 1d realized vol %.2f", ctx.RealizedVol1dPct)
	}
	if ctx.RealizedVol5dPct != 28.1 {
		t.Fatalf("unexpected 5d realized vol %.2f", ctx.RealizedVol5dPct)
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
		quote: model.MarketQuote{
			ObservedAt: time.Now().Add(-15 * time.Second),
			Bid:        18.3,
			Ask:        18.5,
			Volume:     420000,
		},
		vol: map[time.Duration]float64{
			24 * time.Hour: 54.2,
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
	if ctx.MidPrice != 18.4 {
		t.Fatalf("expected midpoint 18.4, got %.2f", ctx.MidPrice)
	}
	if ctx.RealizedVol1dPct != 54.2 {
		t.Fatalf("expected 1d realized vol 54.2, got %.2f", ctx.RealizedVol1dPct)
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
