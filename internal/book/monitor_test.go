package book

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestMonitorClosesShortOptionInsideAssignmentWindow(t *testing.T) {
	book := NewBook(nil, 100000)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.May, 8, 10, 0, 0, 0, loc)
	expiry := now.Format("20060102")
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
		FilledAt:   now.Add(-time.Hour),
	}
	pos := book.OpenPosition(fill, thesis)
	pos.CurrentPrice = 2.55

	var closeReason string
	monitor := NewMonitor(book, func(id string) (*model.Thesis, bool) {
		return thesis, id == thesis.ID
	}, func(_ *model.Position, _ float64, reason string) {
		closeReason = reason
	})
	monitor.now = func() time.Time { return now }

	monitor.RunOnce()
	if closeReason != "assignment_risk" {
		t.Fatalf("expected assignment_risk close, got %q", closeReason)
	}
}

func TestMonitorEmitsPinRiskLifecycleAlert(t *testing.T) {
	book := NewBook(nil, 100000)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.May, 7, 0, 0, 0, 0, loc)
	expiry := now.AddDate(0, 0, 1).Format("20060102")
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
		FilledAt:   now.Add(-time.Hour),
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
	monitor.now = func() time.Time { return now }

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

func TestParseInstrumentExpiryUsesExpirationSessionClose(t *testing.T) {
	expiry, ok := parseInstrumentExpiry("20260508")
	if !ok {
		t.Fatal("expected date-only expiry to parse")
	}

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	local := expiry.In(loc)
	if local.Year() != 2026 || local.Month() != time.May || local.Day() != 8 {
		t.Fatalf("expected 2026-05-08 expiry date, got %s", local.Format(time.RFC3339))
	}
	if local.Hour() != 16 || local.Minute() != 0 || local.Second() != 0 {
		t.Fatalf("expected 16:00 New York expiry close, got %s", local.Format(time.RFC3339))
	}
}
