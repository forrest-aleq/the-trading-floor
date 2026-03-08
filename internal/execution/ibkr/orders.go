package ibkr

import (
	"github.com/hnic/trading-floor/pkg/model"
)

// BuildContract converts our Instrument to IBKR contract params
func BuildContract(inst model.Instrument) Contract {
	return Contract{
		Symbol:     inst.Symbol,
		SecType:    inst.SecType,
		Exchange:   inst.Exchange,
		Currency:   inst.Currency,
		Expiry:     inst.Expiry,
		Strike:     inst.Strike,
		Right:      inst.Right,
		Multiplier: inst.Multiplier,
	}
}

// Contract mirrors ibapi.Contract fields we use
type Contract struct {
	ConID      int64
	Symbol     string
	SecType    string // STK, OPT, FUT, CASH, BOND
	Exchange   string // SMART, NYSE, NASDAQ, GLOBEX, etc.
	Currency   string // USD, EUR, etc.
	Expiry     string // YYYYMMDD for options/futures
	Strike     float64
	Right      string // C or P
	Multiplier string // 100 for options
}

// RouteOrder selects the right order type based on size and instrument
func RouteOrder(order model.Order) OrderParams {
	action := "BUY"
	if order.Direction == model.Short {
		action = "SELL"
	}

	params := OrderParams{
		Action:      action,
		TotalQty:    order.Quantity,
		TimeInForce: order.TimeInForce,
	}
	if params.TimeInForce == "" {
		params.TimeInForce = "DAY"
	}

	// Smart routing based on order size and instrument type
	switch {
	case order.Instrument.SecType == "OPT":
		// Options: use MidPrice for price improvement
		params.OrderType = "MIDPRICE"

	case order.Notional < 10000:
		// Small orders: limit order
		params.OrderType = "LMT"
		params.LmtPrice = order.LimitPrice

	case order.Notional < 100000:
		// Medium: adaptive algo
		params.OrderType = "LMT"
		params.LmtPrice = order.LimitPrice
		params.AlgoStrategy = "Adaptive"
		params.AlgoParams = map[string]string{
			"adaptivePriority": "Normal",
		}

	default:
		// Large: TWAP to minimize impact
		params.OrderType = "LMT"
		params.LmtPrice = order.LimitPrice
		params.AlgoStrategy = "Twap"
		params.AlgoParams = map[string]string{
			"strategyType":  "Marketable",
			"startTime":     "",
			"endTime":       "",
			"allowPastEndTime": "1",
		}
	}

	return params
}

// OrderParams mirrors ibapi.Order fields we use
type OrderParams struct {
	Action        string
	OrderType     string  // MKT, LMT, STP, MIDPRICE
	TotalQty      float64
	LmtPrice      float64
	AuxPrice      float64 // Stop price
	TimeInForce   string  // DAY, GTC, IOC
	AlgoStrategy  string
	AlgoParams    map[string]string
	Transmit      bool
	ParentID      int64    // For bracket orders
	OcaGroup      string   // One-Cancels-All group
}
