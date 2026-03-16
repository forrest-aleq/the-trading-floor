package book

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestMonitorClosesShortOptionInsideAssignmentWindow(t *testing.T) {
	book := NewBook(nil, 100000)
	expiry := time.Now().UTC().Add(20 * time.Hour).Format("20060102")
	option := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   expiry,
		Strike:   140,
		Right:    "C",
	}
	thesis := &model.Thesis{
		ID:         "thesis-assignment",
		DeskID:     "desk-a",
		Structure:  "single",
		Instrument: option,
		Direction:  model.Short,
		EntryPrice: 2.40,
	}
	fill := &model.Fill{
		OrderID:    thesis.ID,
		Instrument: option,
		Direction:  model.Short,
		Quantity:   1,
		AvgPrice:   2.40,
		FilledAt:   time.Now().Add(-time.Hour),
	}
	pos := book.OpenPosition(fill, thesis)
	pos.CurrentPrice = 2.55

	var closeReason string
	monitor := NewMonitor(book, func(id string) (*model.Thesis, bool) {
		return thesis, id == thesis.ID
	}, func(_ *model.Position, _ float64, reason string) {
		closeReason = reason
	})

	monitor.RunOnce()
	if closeReason != "assignment_risk" {
		t.Fatalf("expected assignment_risk close, got %q", closeReason)
	}
}

func TestMonitorEmitsPinRiskLifecycleAlert(t *testing.T) {
	book := NewBook(nil, 100000)
	expiry := time.Now().UTC().Add(40 * time.Hour).Format("20060102")
	lower := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   expiry,
		Strike:   130,
		Right:    "C",
	}
	higher := lower
	higher.Strike = 140
	thesis := &model.Thesis{
		ID:        "thesis-pin",
		DeskID:    "desk-a",
		Structure: "bull_call_spread",
		Legs: []model.TradeLeg{
			{Instrument: lower, Direction: model.Long, Ratio: 1},
			{Instrument: higher, Direction: model.Short, Ratio: 1},
		},
		Instrument: lower,
		Direction:  model.Long,
		EntryPrice: 3.20,
	}
	fill := &model.Fill{
		OrderID:    thesis.ID,
		Structure:  thesis.Structure,
		Instrument: lower,
		Legs:       append([]model.TradeLeg(nil), thesis.Legs...),
		Direction:  model.Long,
		Quantity:   1,
		AvgPrice:   3.20,
		FilledAt:   time.Now().Add(-time.Hour),
	}
	pos := book.OpenPosition(fill, thesis)
	pos.CurrentPrice = 3.10

	var alertKinds []string
	monitor := NewMonitor(book, func(id string) (*model.Thesis, bool) {
		return thesis, id == thesis.ID
	}, nil)
	monitor.SetLifecycleHandler(func(_ *model.Position, alert model.LifecycleAlert) {
		alertKinds = append(alertKinds, alert.Kind)
	})

	monitor.RunOnce()

	if len(alertKinds) == 0 {
		t.Fatal("expected lifecycle alerts to fire for near-expiry spread")
	}
	found := false
	for _, kind := range alertKinds {
		if kind == "pin_risk" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pin_risk alert, got %+v", alertKinds)
	}
}
