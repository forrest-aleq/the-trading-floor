package orderflow

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestCompileEntryLimitOrder(t *testing.T) {
	compiler := NewCompiler()
	now := time.Now().UTC()
	thesis := &model.Thesis{
		ID:           "thesis-1",
		Structure:    "single",
		Instrument:   model.Instrument{Symbol: "NVDA", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		EntryPrice:   123.45,
		StopLoss:     118,
		PositionSize: 10,
		MarketContext: &model.MarketContext{
			SnapshotAt:      now,
			CurrentPrice:    123.2,
			BidPrice:        123.1,
			AskPrice:        123.3,
			MidPrice:        123.2,
			SpreadBps:       16.2,
			QuoteAgeSeconds: 3,
		},
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
	if order.ExecutionIntent == nil {
		t.Fatal("expected execution intent to be attached")
	}
	if order.ExecutionIntent.DecisionPrice != 123.45 {
		t.Fatalf("expected decision price 123.45, got %.2f", order.ExecutionIntent.DecisionPrice)
	}
	if order.ExecutionIntent.ReferencePrice != 123.45 {
		t.Fatalf("expected reference price 123.45, got %.2f", order.ExecutionIntent.ReferencePrice)
	}
	if !order.ExecutionIntent.DecidedAt.Equal(now) {
		t.Fatalf("expected decided at %s, got %s", now, order.ExecutionIntent.DecidedAt)
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
	if order.ExecutionIntent == nil {
		t.Fatal("expected execution intent for market order")
	}
	if order.ExecutionIntent.DecisionPrice != 90 {
		t.Fatalf("expected decision price 90, got %.2f", order.ExecutionIntent.DecisionPrice)
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

func TestCompileEntryUsesAdaptiveForFreshTightSingleNameQuotes(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-adaptive",
		Instrument:   model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		PositionSize: 10,
		MarketContext: &model.MarketContext{
			CurrentPrice:    500.02,
			BidPrice:        500.01,
			AskPrice:        500.03,
			MidPrice:        500.02,
			SpreadBps:       0.4,
			QuoteAgeSeconds: 1,
		},
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-adaptive", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderAdaptive {
		t.Fatalf("expected adaptive order, got %s", order.OrderType)
	}
	if order.LimitPrice != 500.03 {
		t.Fatalf("expected aggressive ask cap 500.03, got %.2f", order.LimitPrice)
	}
}

func TestCompileEntryUsesMidPriceForModerateSpreadQuotes(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-mid",
		Instrument:   model.Instrument{Symbol: "QQQ", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		PositionSize: 5,
		MarketContext: &model.MarketContext{
			CurrentPrice:    400.10,
			BidPrice:        400.00,
			AskPrice:        400.20,
			MidPrice:        400.10,
			SpreadBps:       12.0,
			QuoteAgeSeconds: 2,
		},
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-mid", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderMidPrice {
		t.Fatalf("expected midprice order, got %s", order.OrderType)
	}
	if order.LimitPrice != 400.20 {
		t.Fatalf("expected protective cap 400.20, got %.2f", order.LimitPrice)
	}
}

func TestCompileEntryUsesPassiveLimitWhenSpreadIsWide(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-passive",
		Instrument:   model.Instrument{Symbol: "SMALL", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		PositionSize: 3,
		MarketContext: &model.MarketContext{
			CurrentPrice:    20.5,
			BidPrice:        20.0,
			AskPrice:        21.0,
			MidPrice:        20.5,
			SpreadBps:       243.9,
			QuoteAgeSeconds: 2,
		},
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-passive", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderLimit {
		t.Fatalf("expected passive limit order, got %s", order.OrderType)
	}
	if order.LimitPrice != 20.0 {
		t.Fatalf("expected passive bid 20.0, got %.2f", order.LimitPrice)
	}
}

func TestCompileEntryUpgradesExplicitMidAnchorToMidPrice(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-explicit-mid",
		Instrument:   model.Instrument{Symbol: "NVDA", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		EntryPrice:   123.20,
		PositionSize: 4,
		MarketContext: &model.MarketContext{
			CurrentPrice:    123.2,
			BidPrice:        123.1,
			AskPrice:        123.3,
			MidPrice:        123.2,
			SpreadBps:       16.2,
			QuoteAgeSeconds: 3,
		},
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-explicit-mid", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderMidPrice {
		t.Fatalf("expected midprice order, got %s", order.OrderType)
	}
	if order.LimitPrice != 123.3 {
		t.Fatalf("expected aggressive cap 123.3, got %.2f", order.LimitPrice)
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
