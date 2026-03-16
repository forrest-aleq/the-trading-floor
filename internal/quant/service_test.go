package quant

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestAnalyzeBullCallSpread(t *testing.T) {
	service := NewService()
	thesis := &model.Thesis{
		Structure:    "bull_call_spread",
		PositionSize: 2,
		EntryPrice:   3.5,
		Legs: []model.TradeLeg{
			{
				Instrument: model.Instrument{Symbol: "NVDA", SecType: "OPT", Currency: "USD", Expiry: "20260619", Strike: 120, Right: "C", Multiplier: "100"},
				Direction:  model.Long,
				Ratio:      1,
				EntryPrice: 5.5,
			},
			{
				Instrument: model.Instrument{Symbol: "NVDA", SecType: "OPT", Currency: "USD", Expiry: "20260619", Strike: 130, Right: "C", Multiplier: "100"},
				Direction:  model.Short,
				Ratio:      1,
				EntryPrice: 2.0,
			},
		},
		MarketContext: &model.MarketContext{CurrentPrice: 124},
	}

	metrics := service.AnalyzeThesis(thesis)
	if metrics == nil {
		t.Fatal("expected metrics")
	}
	if !metrics.DefinedRisk {
		t.Fatal("expected defined risk vertical")
	}
	if metrics.MaxLoss != 700 {
		t.Fatalf("expected max loss 700, got %.2f", metrics.MaxLoss)
	}
	if metrics.MaxGain != 1300 {
		t.Fatalf("expected max gain 1300, got %.2f", metrics.MaxGain)
	}
	if metrics.Breakeven != 123.5 {
		t.Fatalf("expected breakeven 123.5, got %.2f", metrics.Breakeven)
	}
	if len(metrics.Scenarios) != 5 {
		t.Fatalf("expected scenarios, got %d", len(metrics.Scenarios))
	}
}

func TestAnalyzeSingleEquityIncludesMarginEstimate(t *testing.T) {
	service := NewService()
	thesis := &model.Thesis{
		Structure:    "single",
		Direction:    model.Long,
		PositionSize: 10,
		EntryPrice:   100,
		TargetPrice:  110,
		StopLoss:     95,
		Instrument:   model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"},
	}

	metrics := service.AnalyzeThesis(thesis)
	if metrics == nil {
		t.Fatal("expected metrics")
	}
	if metrics.MarginEstimate != 1000 {
		t.Fatalf("expected margin estimate 1000, got %.2f", metrics.MarginEstimate)
	}
	if metrics.MaxLoss != 50 {
		t.Fatalf("expected stop-based max loss 50, got %.2f", metrics.MaxLoss)
	}
	if metrics.MaxGain != 100 {
		t.Fatalf("expected target gain 100, got %.2f", metrics.MaxGain)
	}
	if metrics.RewardToRisk != 2 {
		t.Fatalf("expected reward/risk 2, got %.2f", metrics.RewardToRisk)
	}
}
