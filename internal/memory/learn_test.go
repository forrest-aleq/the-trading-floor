package memory

import (
	"testing"

	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/pkg/model"
)

func TestLearnWorkerRecordsGlobalAndDeskEngrams(t *testing.T) {
	graph := belief.NewGraph()
	store := NewEngramStore()
	worker := NewLearnWorker(graph, store)

	thesis := &model.Thesis{
		ID:           "thesis-1",
		DeskID:       "desk-a",
		Domain:       "macro",
		Strategy:     "macro",
		EntryPrice:   100,
		StopLoss:     95,
		PositionSize: 10,
		Instrument: model.Instrument{
			Symbol:   "AAPL",
			SecType:  "STK",
			Currency: "USD",
		},
	}
	outcome := &model.ThesisOutcome{
		Profitable:  true,
		RealizedPnL: 200,
	}
	regime := model.Regime{
		Volatility: "medium",
		Trend:      "neutral",
		Risk:       "risk_on",
		Liquidity:  "normal",
	}

	worker.ProcessOutcome(thesis, outcome, regime)

	state, ok := graph.Lookup("desk-a", "scan", "macro", regime.Key())
	if !ok {
		t.Fatal("expected scan competence state to be recorded")
	}
	if state.SuccessCount != 1 {
		t.Fatalf("expected scan competence success count to be 1, got %d", state.SuccessCount)
	}

	stats := store.Stats()
	if stats.Total != 2 {
		t.Fatalf("expected 2 engrams, got %+v", stats)
	}
	if stats.Global != 1 || stats.DeskSpecific != 1 {
		t.Fatalf("expected 1 global and 1 desk-specific engram, got %+v", stats)
	}

	engrams := store.Lookup("macro_STK", "desk-a")
	if len(engrams) != 2 {
		t.Fatalf("expected desk lookup to include desk + global engrams, got %d", len(engrams))
	}
	if engrams[0].DeskID != "desk-a" {
		t.Fatalf("expected desk-specific engram first, got desk_id=%q", engrams[0].DeskID)
	}
	if engrams[1].Layer != 1 {
		t.Fatalf("expected global engram second, got layer=%d", engrams[1].Layer)
	}
}

func TestLearnWorkerUsesAttributionDimensions(t *testing.T) {
	graph := belief.NewGraph()
	worker := NewLearnWorker(graph, nil)

	thesis := &model.Thesis{
		ID:           "thesis-2",
		DeskID:       "desk-a",
		Domain:       "volatility",
		Strategy:     "event",
		Structure:    "bull_call_spread",
		EntryPrice:   4,
		StopLoss:     2,
		PositionSize: 5,
		Instrument: model.Instrument{
			Symbol:   "NVDA",
			SecType:  "OPT",
			Currency: "USD",
		},
	}
	outcome := &model.ThesisOutcome{
		Profitable:  true,
		RealizedPnL: 600,
		ExitReason:  "target_hit",
		Attribution: &model.OutcomeAttribution{
			TruthEdge:      0.90,
			TimingEdge:     -0.20,
			ExpressionEdge: 0.70,
			ExecutionEdge:  0.10,
			LuckEstimate:   0.10,
		},
	}
	regime := model.Regime{
		Volatility: "high",
		Trend:      "trending_up",
		Risk:       "risk_on",
		Liquidity:  "normal",
	}

	worker.ProcessOutcome(thesis, outcome, regime)

	truthState, ok := graph.Lookup("desk-a", competenceThesisAssessment, "event", regime.Key())
	if !ok || truthState.SuccessCount != 1 {
		t.Fatalf("expected thesis competence success, got %+v", truthState)
	}

	timingState, ok := graph.Lookup("desk-a", competenceTimingAssessment, "event", regime.Key())
	if !ok || timingState.FailureCount != 1 {
		t.Fatalf("expected timing competence failure, got %+v", timingState)
	}

	structureState, ok := graph.Lookup("desk-a", competenceStructureSelect, thesis.ExecutionCapability(), regime.Key())
	if !ok || structureState.SuccessCount != 1 {
		t.Fatalf("expected structure competence success, got %+v", structureState)
	}

	if outcome.Attribution == nil || len(outcome.Attribution.CompetenceUpdates) < 4 {
		t.Fatalf("expected competence updates on attribution, got %+v", outcome.Attribution)
	}
}

func TestLearnWorkerSkipsBeliefHardeningForLuckDrivenWins(t *testing.T) {
	graph := belief.NewGraph()
	store := NewEngramStore()
	worker := NewLearnWorker(graph, store)

	thesis := &model.Thesis{
		ID:           "thesis-3",
		DeskID:       "desk-a",
		Domain:       "macro",
		Strategy:     "macro",
		EntryPrice:   100,
		StopLoss:     95,
		PositionSize: 10,
		Instrument: model.Instrument{
			Symbol:   "TLT",
			SecType:  "STK",
			Currency: "USD",
		},
	}
	outcome := &model.ThesisOutcome{
		Profitable:  true,
		RealizedPnL: 40,
		Attribution: &model.OutcomeAttribution{
			TruthEdge:      0.20,
			TimingEdge:     0.10,
			ExpressionEdge: 0.10,
			ExecutionEdge:  0.05,
			LuckEstimate:   0.98,
		},
	}
	regime := model.Regime{
		Volatility: "low",
		Trend:      "neutral",
		Risk:       "risk_on",
		Liquidity:  "normal",
	}

	worker.ProcessOutcome(thesis, outcome, regime)

	if _, ok := graph.Lookup("desk-a", competenceThesisAssessment, "macro", regime.Key()); ok {
		t.Fatal("expected no competence update for luck-driven win")
	}

	if stats := store.Stats(); stats.Total != 0 {
		t.Fatalf("expected no engram hardening for luck-driven win, got %+v", stats)
	}
}
