package risk

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
)

func TestGateAllowsDefinedRiskBullCallSpreadUsingMaxLoss(t *testing.T) {
	gate := NewGate(DefaultLimits())

	lower := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   "20260619",
		Strike:   120,
		Right:    "C",
	}
	higher := lower
	higher.Strike = 130

	order := model.Order{
		ID:         "spread-1",
		DeskID:     "desk-a",
		Structure:  "bull_call_spread",
		Direction:  model.Long,
		Quantity:   1,
		LimitPrice: 3.50,
		Notional:   15000,
		Legs: []model.TradeLeg{
			{Instrument: lower, Direction: model.Long, Ratio: 1},
			{Instrument: higher, Direction: model.Short, Ratio: 1},
		},
	}
	thesis := &model.Thesis{Conviction: 0.9}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if !decision.Allowed {
		t.Fatalf("expected defined-risk bull call spread to be allowed, got %+v", decision.Violations)
	}
}

func TestGateRejectsUnsupportedMultiLegStructure(t *testing.T) {
	gate := NewGate(DefaultLimits())

	callJun := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   "20260619",
		Strike:   120,
		Right:    "C",
	}
	callSep := callJun
	callSep.Expiry = "20260918"

	order := model.Order{
		ID:         "spread-2",
		DeskID:     "desk-a",
		Structure:  "calendar_spread",
		Direction:  model.Long,
		Quantity:   1,
		LimitPrice: 4.0,
		Notional:   400,
		Legs: []model.TradeLeg{
			{Instrument: callJun, Direction: model.Long, Ratio: 1},
			{Instrument: callSep, Direction: model.Short, Ratio: 1},
		},
	}
	thesis := &model.Thesis{Conviction: 0.9}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected unsupported multi-leg structure to be rejected")
	}
	if len(decision.Violations) == 0 || decision.Violations[0].Rule != "unsupported_multi_leg_structure" {
		t.Fatalf("expected unsupported multi-leg violation, got %+v", decision.Violations)
	}
}

func TestGateRejectsStaleEvidence(t *testing.T) {
	gate := NewGate(DefaultLimits())

	order := model.Order{
		ID:         "order-1",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   10,
		LimitPrice: 100,
		Notional:   1000,
	}
	thesis := &model.Thesis{
		Conviction: 0.9,
		EvidenceMeta: &evidence.Metadata{
			SourceTrust:        0.86,
			FreshnessStatus:    "stale",
			FreshnessReason:    "stale_news",
			EvidenceScore:      0.22,
			ContradictionCount: 0,
		},
	}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected stale evidence to be rejected")
	}

	found := false
	for _, violation := range decision.Violations {
		if violation.Rule == "stale_signal_evidence" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected stale_signal_evidence violation, got %+v", decision.Violations)
	}
}
