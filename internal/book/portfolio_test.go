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
