package risk

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func drawdownGateOrder() model.Order {
	return model.Order{
		ID:         "order-dd",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   5,
		LimitPrice: 100,
		Notional:   500,
	}
}

func drawdownPortfolioState(currentDDPct, maxDDPct float64) PortfolioState {
	return PortfolioState{
		NAV:                900000,
		Cash:               900000,
		CurrentDrawdownPct: currentDDPct,
		MaxDrawdownPct:     maxDDPct,
		DeskPositions:      map[string]int{},
		DeskDailyPnL:       map[string]float64{},
		DeskCapital:        map[string]float64{"desk-a": 25000},
	}
}

func decisionHasViolation(d model.RiskDecision, rule string) bool {
	for _, v := range d.Violations {
		if v.Rule == rule {
			return true
		}
	}
	return false
}

// Guards the profit-then-crash hole: a book that ran up and then fell 29% off
// its peak while only 12% down from initial capital must still trip the kill
// switch, which the old MonthlyPnL/TotalCapital check never saw.
func TestKillSwitchFiresOnDrawdownFromPeakAfterProfitableRun(t *testing.T) {
	gate := NewGate(DefaultLimits())

	decision := gate.Check(drawdownGateOrder(), &model.Thesis{Conviction: 0.9}, drawdownPortfolioState(29.6, 29.6))

	if decision.Allowed {
		t.Fatal("expected order rejection at 29.6% drawdown from peak")
	}
	if !decisionHasViolation(decision, "KILL_SWITCH") {
		t.Fatalf("expected KILL_SWITCH violation, got %+v", decision.Violations)
	}
}

func TestKillSwitchLatchesOnMaxDrawdownAfterRecovery(t *testing.T) {
	gate := NewGate(DefaultLimits())

	decision := gate.Check(drawdownGateOrder(), &model.Thesis{Conviction: 0.9}, drawdownPortfolioState(8.0, 16.0))

	if decision.Allowed {
		t.Fatal("expected kill switch to stay latched after a 16% max drawdown")
	}
	if !decisionHasViolation(decision, "KILL_SWITCH") {
		t.Fatalf("expected KILL_SWITCH violation, got %+v", decision.Violations)
	}
}

func TestMaxDrawdownBrakeBlocksNewEntries(t *testing.T) {
	limits := DefaultLimits()
	gate := NewGate(limits)
	betweenBrakeAndKill := (limits.MaxDrawdownPct + limits.KillSwitchDrawdownPct) / 2

	decision := gate.Check(drawdownGateOrder(), &model.Thesis{Conviction: 0.9},
		drawdownPortfolioState(betweenBrakeAndKill, betweenBrakeAndKill))

	if decision.Allowed {
		t.Fatalf("expected order rejection at %.1f%% current drawdown", betweenBrakeAndKill)
	}
	if !decisionHasViolation(decision, "max_drawdown") {
		t.Fatalf("expected max_drawdown violation, got %+v", decision.Violations)
	}
	if decisionHasViolation(decision, "KILL_SWITCH") {
		t.Fatalf("%.1f%% drawdown is below the kill switch threshold, got %+v", betweenBrakeAndKill, decision.Violations)
	}
}

func TestGateAllowsModestDrawdown(t *testing.T) {
	limits := DefaultLimits()
	gate := NewGate(limits)
	belowBrake := limits.MaxDrawdownPct / 2

	decision := gate.Check(drawdownGateOrder(), &model.Thesis{Conviction: 0.9},
		drawdownPortfolioState(belowBrake, belowBrake))

	if !decision.Allowed {
		t.Fatalf("expected order to pass at %.1f%% drawdown, got %+v", belowBrake, decision.Violations)
	}
}
