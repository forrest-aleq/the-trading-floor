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

func TestCompileEntryUsesAggressiveReferenceLimitWhenOnlyLastPriceIsAvailable(t *testing.T) {
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
	if order.OrderType != model.OrderLimit {
		t.Fatalf("expected limit order, got %s", order.OrderType)
	}
	if order.LimitPrice != 89.55 {
		t.Fatalf("expected aggressive reference limit 89.55, got %.2f", order.LimitPrice)
	}
	if order.Notional <= 0 {
		t.Fatalf("expected positive notional from market context, got %.2f", order.Notional)
	}
	if order.ExecutionIntent == nil {
		t.Fatal("expected execution intent for reference-limit order")
	}
	if order.ExecutionIntent.DecisionPrice != 90 {
		t.Fatalf("expected decision price 90, got %.2f", order.ExecutionIntent.DecisionPrice)
	}
}

func TestCompileEntryUsesMarketOrderWhenNoReferencePriceExists(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-no-price",
		Instrument:   model.Instrument{Symbol: "TLT", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		PositionSize: 5,
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-b", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderMarket {
		t.Fatalf("expected market order, got %s", order.OrderType)
	}
	if order.Notional != 0 {
		t.Fatalf("expected zero notional without reference price, got %.2f", order.Notional)
	}
}

func TestCompileEntryRejectsPredictionMarketInstrumentForBrokerExecution(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-kalshi-leak",
		Instrument:   model.Instrument{Symbol: "KXTHORNE", SecType: "STK", Currency: "USD"},
		Direction:    model.Short,
		EntryPrice:   0.42,
		PositionSize: 1,
	}

	if _, err := compiler.CompileEntry(EntryInput{DeskID: "corp-earnings-a", Thesis: thesis}); err == nil {
		t.Fatal("expected broker compiler to reject prediction-market instrument")
	}
}

func TestCompileEntryRoundsExplicitStockLimitToTick(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-rounded",
		Instrument:   model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"},
		Direction:    model.Short,
		EntryPrice:   276.2357,
		PositionSize: 1,
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-rounded", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderLimit {
		t.Fatalf("expected limit order, got %s", order.OrderType)
	}
	if order.LimitPrice != 276.24 {
		t.Fatalf("expected rounded stock limit 276.24, got %.4f", order.LimitPrice)
	}
}

func TestCompileEntryReplacesImplausibleExplicitStockEntryWithFreshQuote(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-bad-entry",
		Instrument:   model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"},
		Direction:    model.Short,
		EntryPrice:   0.50,
		PositionSize: 1,
		MarketContext: &model.MarketContext{
			CurrentPrice:    200.00,
			BidPrice:        199.90,
			AskPrice:        200.10,
			MidPrice:        200.00,
			SpreadBps:       10,
			QuoteAgeSeconds: 2,
		},
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-bad-entry", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderLimit {
		t.Fatalf("expected limit order, got %s", order.OrderType)
	}
	if order.LimitPrice != 199.80 {
		t.Fatalf("expected marketable bid-side limit 199.80, got %.2f", order.LimitPrice)
	}
}

func TestCompileEntryReplacesImplausibleExplicitStockEntryWithReferenceLimit(t *testing.T) {
	compiler := NewCompiler()
	thesis := &model.Thesis{
		ID:           "thesis-bad-entry-current",
		Instrument:   model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"},
		Direction:    model.Short,
		EntryPrice:   0.50,
		PositionSize: 1,
		MarketContext: &model.MarketContext{
			CurrentPrice: 200.00,
		},
	}

	order, err := compiler.CompileEntry(EntryInput{DeskID: "desk-bad-entry-current", Thesis: thesis})
	if err != nil {
		t.Fatalf("compile entry: %v", err)
	}
	if order.OrderType != model.OrderLimit {
		t.Fatalf("expected limit order, got %s", order.OrderType)
	}
	if order.LimitPrice != 199.00 {
		t.Fatalf("expected aggressive current-price limit 199.00, got %.2f", order.LimitPrice)
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
	if order.LimitPrice != 500.28 {
		t.Fatalf("expected aggressive ask cap 500.28, got %.2f", order.LimitPrice)
	}
}

func TestCompileEntryUsesMarketableLimitForModerateSpreadQuotes(t *testing.T) {
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
	if order.OrderType != model.OrderLimit {
		t.Fatalf("expected limit order, got %s", order.OrderType)
	}
	if order.LimitPrice != 400.40 {
		t.Fatalf("expected protective cap 400.40, got %.2f", order.LimitPrice)
	}
}

func TestCompileEntryUsesMarketableLimitWhenSpreadIsWide(t *testing.T) {
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
		t.Fatalf("expected marketable limit order, got %s", order.OrderType)
	}
	if order.LimitPrice != 21.01 {
		t.Fatalf("expected marketable ask cap 21.01, got %.2f", order.LimitPrice)
	}
}

func TestCompileEntryUpgradesExplicitMidAnchorToMarketableLimit(t *testing.T) {
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
	if order.OrderType != model.OrderLimit {
		t.Fatalf("expected limit order, got %s", order.OrderType)
	}
	if order.LimitPrice != 123.36 {
		t.Fatalf("expected aggressive cap 123.36, got %.2f", order.LimitPrice)
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
