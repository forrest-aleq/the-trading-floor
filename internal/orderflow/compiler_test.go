package orderflow

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestCompileEntryLimitOrder(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-1",
		Structure:    "single",
		Instrument:   model.Instrument{Symbol: "NVDA", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		EntryPrice:   123.45,
		StopLoss:     118,
		PositionSize: 10,
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-a", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderLimit {
		t.Fatalf("expected limit order, got %s", order.OrderType)
	}
	if order.Quantity != 10 {
		t.Fatalf("expected quantity 10, got %.2f", order.Quantity)
	}
	if order.Notional <= 0 {
		t.Fatalf("expected positive notional, got %.2f", order.Notional)
	}
}

func TestCompileEntryFallsBackToMarketOrder(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-2",
		Instrument:   model.Instrument{Symbol: "TLT", SecType: "STK", Currency: "USD"},
		Direction:    model.Short,
		PositionSize: 5,
		MarketContext: &model.MarketContext{
			CurrentPrice: 90,
		},
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-b", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderMarket {
		t.Fatalf("expected market order, got %s", order.OrderType)
	}
	if order.Notional <= 0 {
		t.Fatalf("expected positive notional from market context, got %.2f", order.Notional)
	}
}

func TestCompileEntryPreservesMultiLegStructure(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:        "combo-1",
		Structure: "bull_call_spread",
		Legs: []model.TradeLeg{
			{
				Instrument: model.Instrument{Symbol: "NVDA", SecType: "OPT", Currency: "USD", Expiry: "20260515", Strike: 130, Right: "C", Multiplier: "100"},
				Direction:  model.Long,
				Ratio:      1,
				EntryPrice: 5.5,
			},
			{
				Instrument: model.Instrument{Symbol: "NVDA", SecType: "OPT", Currency: "USD", Expiry: "20260515", Strike: 140, Right: "C", Multiplier: "100"},
				Direction:  model.Short,
				Ratio:      1,
				EntryPrice: 2.0,
			},
		},
		Direction:    model.Long,
		EntryPrice:   3.5,
		StopLoss:     1.2,
		PositionSize: 2,
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-c", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if !order.IsMultiLeg() {
		t.Fatal("expected multileg order")
	}
	if got := len(order.Legs); got != 2 {
		t.Fatalf("expected 2 legs, got %d", got)
	}
	if order.Structure != "bull_call_spread" {
		t.Fatalf("expected structure to survive, got %q", order.Structure)
	}
}

func TestCompileExitBuildsFlatteningOrder(t *testing.T) {
	compiler := NewCompiler()
	pos := &model.Position{
		ID:         "pos-1",
		ThesisID:   "thesis-1",
		DeskID:     "desk-a",
		Structure:  "single",
		Instrument: model.Instrument{Symbol: "NVDA", SecType: "STK", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   8,
	}

	order, err := compiler.CompileExit(pos)
	if err != nil {
		t.Fatalf("compile exit: %v", err)
	}
	if order.Direction != model.Short {
		t.Fatalf("expected exit direction short, got %s", order.Direction)
	}
	if order.OrderType != model.OrderMarket {
		t.Fatalf("expected market exit, got %s", order.OrderType)
	}
	if order.Quantity != 8 {
		t.Fatalf("expected quantity 8, got %.2f", order.Quantity)
	}
}
