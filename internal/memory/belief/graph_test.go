package belief

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestAssessTerritory(t *testing.T) {
	graph := NewGraph()
	regime := model.Regime{
		Volatility: "medium",
		Trend:      "neutral",
		Risk:       "risk_on",
		Liquidity:  "normal",
	}

	unknown := graph.AssessTerritory("desk-a", "macro", "STK", regime.Key(), 20)
	if unknown.Status != TerritoryUnknown {
		t.Fatalf("expected unknown territory, got %s", unknown.Status)
	}

	graph.Load([]*model.CompetenceState{{
		Key:          CompetenceKey("desk-a", "macro", "STK", "high:neutral:risk_off:stressed"),
		DeskID:       "desk-a",
		Capability:   "macro",
		Context:      "STK",
		Regime:       "high:neutral:risk_off:stressed",
		Trust:        0.66,
		Confidence:   0.55,
		SuccessCount: 35,
		FailureCount: 10,
		Autonomy:     model.Supervised,
	}})

	adjacent := graph.AssessTerritory("desk-a", "macro", "STK", regime.Key(), 20)
	if adjacent.Status != TerritoryAdjacent {
		t.Fatalf("expected adjacent territory, got %s", adjacent.Status)
	}

	graph.Load([]*model.CompetenceState{{
		Key:          CompetenceKey("desk-a", "macro", "STK", regime.Key()),
		DeskID:       "desk-a",
		Capability:   "macro",
		Context:      "STK",
		Regime:       regime.Key(),
		Trust:        0.88,
		Confidence:   0.78,
		SuccessCount: 110,
		FailureCount: 15,
		Autonomy:     model.Autonomous,
	}})

	known := graph.AssessTerritory("desk-a", "macro", "STK", regime.Key(), 20)
	if known.Status != TerritoryKnown {
		t.Fatalf("expected known territory, got %s", known.Status)
	}
	if known.Exact == nil || known.Exact.Autonomy != model.Autonomous {
		t.Fatalf("expected known exact state with autonomous mode, got %+v", known.Exact)
	}
}

func TestPeerBeliefUpdates(t *testing.T) {
	graph := NewGraph()
	regime := model.Regime{
		Volatility: "medium",
		Trend:      "neutral",
		Risk:       "risk_on",
		Liquidity:  "normal",
	}

	key := PeerBeliefKey("desk-geo-a", "desk-macro-a", "macro", regime.Key())
	graph.ApplyPeerSuccess(key, 1.0)

	state, ok := graph.LookupPeer("desk-geo-a", "desk-macro-a", "macro", regime.Key())
	if !ok {
		t.Fatal("expected peer belief to be created")
	}
	if state.SuccessCount != 1 {
		t.Fatalf("expected peer success count 1, got %+v", state)
	}
	baselineTrust := state.Trust

	graph.ApplyPeerFailure(key, 1.0)
	state, ok = graph.LookupPeer("desk-geo-a", "desk-macro-a", "macro", regime.Key())
	if !ok {
		t.Fatal("expected peer belief to remain available")
	}
	if state.FailureCount != 1 {
		t.Fatalf("expected peer failure count 1, got %+v", state)
	}
	if state.Trust >= baselineTrust {
		t.Fatalf("expected peer trust to fall after failure, got %.4f >= %.4f", state.Trust, baselineTrust)
	}
	if len(graph.AllPeerBeliefs()) != 1 {
		t.Fatalf("expected one peer belief record, got %d", len(graph.AllPeerBeliefs()))
	}
}
