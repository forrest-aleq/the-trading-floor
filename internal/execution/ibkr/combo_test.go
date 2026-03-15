package ibkr

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestBuildOrderSupportsVerticalSpreadLimitOrder(t *testing.T) {
	order := model.Order{
		ID:          "combo-1",
		Structure:   "bull_call_spread",
		Direction:   model.Long,
		Quantity:    1,
		OrderType:   model.OrderLimit,
		LimitPrice:  2.50,
		TimeInForce: "DAY",
		Legs: []model.TradeLeg{
			{
				Instrument: model.Instrument{
					Symbol:   "NVDA",
					SecType:  "OPT",
					Exchange: "SMART",
					Currency: "USD",
					Expiry:   "20260619",
					Strike:   120,
					Right:    "C",
				},
				Direction: model.Long,
				Ratio:     1,
			},
			{
				Instrument: model.Instrument{
					Symbol:   "NVDA",
					SecType:  "OPT",
					Exchange: "SMART",
					Currency: "USD",
					Expiry:   "20260619",
					Strike:   130,
					Right:    "C",
				},
				Direction: model.Short,
				Ratio:     1,
			},
		},
	}

	ibOrder, err := buildOrder(order)
	if err != nil {
		t.Fatalf("buildOrder returned error: %v", err)
	}
	if ibOrder.Action != "BUY" {
		t.Fatalf("expected BUY combo order, got %q", ibOrder.Action)
	}
	if ibOrder.OrderType != string(model.OrderLimit) {
		t.Fatalf("expected limit order type, got %q", ibOrder.OrderType)
	}
	if ibOrder.TIF != "DAY" {
		t.Fatalf("expected DAY tif, got %q", ibOrder.TIF)
	}
}
