package book

import (
	"context"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

type stubPositionSource struct{}

func (stubPositionSource) GetPositions(ctx context.Context) ([]ibkr.IBKRPosition, error) {
	return nil, nil
}

func TestBookMarkKeepsEquityAndUpdatesPnL(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 1000)
	bk.SetDeskCapital("desk-1", 1000)

	inst := model.Instrument{
		Symbol:   "AAPL",
		SecType:  "STK",
		Currency: "USD",
	}

	bk.OpenPosition(&model.Fill{
		OrderID:    "order-1",
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   10,
		AvgPrice:   10,
		FilledAt:   time.Now(),
	}, &model.Thesis{
		ID:         "thesis-1",
		DeskID:     "desk-1",
		Instrument: inst,
		Direction:  model.Long,
	})

	snapshot := bk.Snapshot()
	if snapshot.NAV != 1000 {
		t.Fatalf("expected unchanged NAV after opening long, got %.2f", snapshot.NAV)
	}

	bk.Mark(map[string]float64{"AAPL": 12})
	snapshot = bk.Snapshot()

	if snapshot.NAV != 1020 {
		t.Fatalf("expected NAV 1020 after mark, got %.2f", snapshot.NAV)
	}
	if snapshot.GrossExposure != 120 {
		t.Fatalf("expected gross exposure 120, got %.2f", snapshot.GrossExposure)
	}
	if snapshot.NetExposure != 120 {
		t.Fatalf("expected net exposure 120, got %.2f", snapshot.NetExposure)
	}
}

func TestBookMarksMultiLegVerticalSpread(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 1000)
	bk.SetDeskCapital("desk-1", 1000)

	longCall := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   "20260619",
		Strike:   120,
		Right:    "C",
	}
	shortCall := longCall
	shortCall.Strike = 130

	fill := &model.Fill{
		OrderID:    "spread-1",
		Structure:  "bull_call_spread",
		Instrument: longCall,
		Legs: []model.TradeLeg{
			{Instrument: longCall, Direction: model.Long, Ratio: 1, Quantity: 1, EntryPrice: 4.0},
			{Instrument: shortCall, Direction: model.Short, Ratio: 1, Quantity: 1, EntryPrice: 1.5},
		},
		Direction: model.Long,
		Quantity:  1,
		AvgPrice:  2.5,
		FilledAt:  time.Now(),
	}
	thesis := &model.Thesis{
		ID:         "thesis-2",
		DeskID:     "desk-1",
		Structure:  "bull_call_spread",
		Instrument: longCall,
		Legs: []model.TradeLeg{
			{Instrument: longCall, Direction: model.Long, Ratio: 1, EntryPrice: 4.0},
			{Instrument: shortCall, Direction: model.Short, Ratio: 1, EntryPrice: 1.5},
		},
		Direction: model.Long,
	}

	bk.OpenPosition(fill, thesis)
	bk.Mark(map[string]float64{
		longCall.Key():  5.5,
		shortCall.Key(): 2.0,
	})

	open := bk.GetOpenPositions()
	if len(open) != 1 {
		t.Fatalf("expected one open position, got %d", len(open))
	}
	if got := open[0].CurrentPrice; got != 3.5 {
		t.Fatalf("expected net combo price 3.5, got %.2f", got)
	}
	if got := open[0].UnrealizedPnL; got != 100 {
		t.Fatalf("expected unrealized pnl 100, got %.2f", got)
	}

	snapshot := bk.Snapshot()
	if snapshot.NAV != 1100 {
		t.Fatalf("expected NAV 1100 after combo mark, got %.2f", snapshot.NAV)
	}
	if snapshot.GrossExposure != 750 {
		t.Fatalf("expected gross exposure 750, got %.2f", snapshot.GrossExposure)
	}
}

func TestOpenShadowPositionUsesPositiveFallbackPrice(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 1000)
	thesis := &model.Thesis{
		ID:           "shadow-1",
		DeskID:       "desk-1",
		Instrument:   model.Instrument{Symbol: "TLT", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:    model.Long,
		PositionSize: 0.01,
		MarketContext: &model.MarketContext{
			CurrentPrice: 91.25,
		},
	}

	pos := bk.OpenShadowPosition(thesis)
	if pos.EntryPrice != 91.25 {
		t.Fatalf("expected shadow entry price from market context, got %.2f", pos.EntryPrice)
	}
	if pos.CurrentPrice != 91.25 {
		t.Fatalf("expected shadow current price from market context, got %.2f", pos.CurrentPrice)
	}

	thesis.EntryPrice = 0
	thesis.MarketContext = nil
	thesis.TargetPrice = 0
	thesis.StopLoss = 0
	thesis.ID = "shadow-2"
	pos = bk.OpenShadowPosition(thesis)
	if pos.EntryPrice <= 0 {
		t.Fatalf("expected positive minimum fallback entry price, got %.4f", pos.EntryPrice)
	}
}
