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
		SurpriseAssessment: &model.SurpriseAssessment{
			TruthScore:        0.90,
			NoveltyScore:      0.85,
			PricedInScore:     0.20,
			ReactionGapScore:  0.75,
			UnmovedAssetScore: 0.60,
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

	surpriseState, ok := graph.Lookup("desk-a", competenceSurpriseAssess, "event", regime.Key())
	if !ok || surpriseState.SuccessCount != 1 {
		t.Fatalf("expected surprise competence success, got %+v", surpriseState)
	}

	if outcome.Attribution == nil || len(outcome.Attribution.CompetenceUpdates) < 5 {
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

func TestLearnWorkerUpdatesPeerBeliefsFromCollaborationInput(t *testing.T) {
	graph := belief.NewGraph()
	worker := NewLearnWorker(graph, nil)

	thesis := &model.Thesis{
		ID:           "thesis-4",
		DeskID:       "desk-macro-a",
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
		CollaborationInput: &model.CollaborationInput{
			OriginDesk:     "desk-geo-a",
			OriginDomain:   "macro",
			OriginThesisID: "thesis-root",
		},
	}
	outcome := &model.ThesisOutcome{
		Profitable:  true,
		RealizedPnL: 200,
		Attribution: &model.OutcomeAttribution{
			TruthEdge:      0.80,
			TimingEdge:     0.30,
			ExpressionEdge: 0.20,
			ExecutionEdge:  0.10,
			LuckEstimate:   0.10,
		},
	}
	regime := model.Regime{
		Volatility: "medium",
		Trend:      "neutral",
		Risk:       "risk_on",
		Liquidity:  "normal",
	}

	worker.ProcessOutcome(thesis, outcome, regime)

	peer, ok := graph.LookupPeer("desk-geo-a", "desk-macro-a", "macro", regime.Key())
	if !ok {
		t.Fatal("expected peer belief to be updated")
	}
	if peer.SuccessCount != 1 {
		t.Fatalf("expected peer belief success count to be 1, got %+v", peer)
	}
	if outcome.Attribution == nil {
		t.Fatal("expected attribution to remain attached")
	}
	foundPeerUpdate := false
	for _, update := range outcome.Attribution.CompetenceUpdates {
		if update.Dimension == "peer_edge" {
			foundPeerUpdate = true
			break
		}
	}
	if !foundPeerUpdate {
		t.Fatalf("expected peer_edge competence update, got %+v", outcome.Attribution.CompetenceUpdates)
	}
}
