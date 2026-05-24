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

func TestDropAutonomyOnlyDemotesRequestedRegime(t *testing.T) {
	graph := NewGraph()
	targetRegime := "medium:neutral:risk_on:normal"
	otherRegime := "high:neutral:risk_off:stressed"
	graph.Load([]*model.CompetenceState{
		{
			Key:          CompetenceKey("desk-a", "macro", "STK", targetRegime),
			DeskID:       "desk-a",
			Capability:   "macro",
			Context:      "STK",
			Regime:       targetRegime,
			Trust:        0.88,
			Confidence:   0.78,
			SuccessCount: 40,
			Autonomy:     model.Autonomous,
		},
		{
			Key:          CompetenceKey("desk-a", "macro", "STK", otherRegime),
			DeskID:       "desk-a",
			Capability:   "macro",
			Context:      "STK",
			Regime:       otherRegime,
			Trust:        0.88,
			Confidence:   0.78,
			SuccessCount: 40,
			Autonomy:     model.Autonomous,
		},
	})

	graph.DropAutonomy(targetRegime)

	target, _ := graph.Lookup("desk-a", "macro", "STK", targetRegime)
	if target.Autonomy != model.Supervised {
		t.Fatalf("expected target regime to be demoted, got %+v", target)
	}
	other, _ := graph.Lookup("desk-a", "macro", "STK", otherRegime)
	if other.Autonomy != model.Autonomous {
		t.Fatalf("expected other regime to remain autonomous, got %+v", other)
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
	baselineHealth := state.RelationshipHealth

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
	if state.RelationshipHealth >= baselineHealth {
		t.Fatalf("expected relationship health to fall after failure, got %.4f >= %.4f", state.RelationshipHealth, baselineHealth)
	}
	if len(graph.AllPeerBeliefs()) != 1 {
		t.Fatalf("expected one peer belief record, got %d", len(graph.AllPeerBeliefs()))
	}
}

func TestPeerBeliefRecoveryAndViolations(t *testing.T) {
	graph := NewGraph()
	regime := model.Regime{
		Volatility: "medium",
		Trend:      "neutral",
		Risk:       "risk_on",
		Liquidity:  "normal",
	}

	key := PeerBeliefKey("desk-geo-a", "desk-macro-a", "macro", regime.Key())
	graph.ApplyPeerSuccessWithContext(key, 1.25, true, 0.55)

	state, ok := graph.LookupPeer("desk-geo-a", "desk-macro-a", "macro", regime.Key())
	if !ok {
		t.Fatal("expected peer belief to be created")
	}
	if state.PositiveRecoveries != 1 {
		t.Fatalf("expected one positive recovery, got %+v", state)
	}
	if state.RecoveryScore <= 0 {
		t.Fatalf("expected recovery score increase, got %+v", state)
	}
	if state.RelationshipHealth <= 0.50 {
		t.Fatalf("expected relationship health above default, got %+v", state)
	}

	graph.ApplyPeerFailureWithContext(key, 1.10, 0.35)
	state, ok = graph.LookupPeer("desk-geo-a", "desk-macro-a", "macro", regime.Key())
	if !ok {
		t.Fatal("expected peer belief to remain available")
	}
	if state.NegativeViolations != 1 {
		t.Fatalf("expected one negative violation, got %+v", state)
	}
}

func TestSourceBeliefUpdates(t *testing.T) {
	graph := NewGraph()

	key := SourceBeliefKey("thomson_reuters", "reuters.com", "macro", "ar", "mena")
	graph.ApplySourceSuccess(key, 1.0)

	state, ok := graph.LookupSource("thomson_reuters", "reuters.com", "macro", "ar", "mena")
	if !ok {
		t.Fatal("expected source belief to be created")
	}
	if state.SuccessCount != 1 {
		t.Fatalf("expected source success count 1, got %+v", state)
	}
	baselineTrust := state.Trust

	graph.ApplySourceFailure(key, 1.0)
	state, ok = graph.LookupSource("thomson_reuters", "reuters.com", "macro", "ar", "mena")
	if !ok {
		t.Fatal("expected source belief to remain available")
	}
	if state.FailureCount != 1 {
		t.Fatalf("expected source failure count 1, got %+v", state)
	}
	if state.Trust >= baselineTrust {
		t.Fatalf("expected source trust to fall after failure, got %.4f >= %.4f", state.Trust, baselineTrust)
	}
	if len(graph.AllSourceBeliefs()) != 1 {
		t.Fatalf("expected one source belief record, got %d", len(graph.AllSourceBeliefs()))
	}
}

func TestLoadPeerAndSourceBeliefs(t *testing.T) {
	graph := NewGraph()
	peerKey := PeerBeliefKey("desk-geo-a", "desk-macro-a", "macro", "medium:neutral:risk_on:normal")
	sourceKey := SourceBeliefKey("thomson_reuters", "reuters.com", "macro", "ar", "mena")

	graph.LoadPeerBeliefs([]*model.DeskRelationshipBelief{{
		Key:           peerKey,
		OriginDesk:    "desk-geo-a",
		ReceivingDesk: "desk-macro-a",
		Domain:        "macro",
		Regime:        "medium:neutral:risk_on:normal",
		Trust:         0.71,
		Confidence:    0.63,
		SuccessCount:  4,
	}})
	graph.LoadSourceBeliefs([]*model.SourceReliabilityBelief{{
		Key:          sourceKey,
		OwnerGroup:   "thomson_reuters",
		SourceDomain: "reuters.com",
		SignalDomain: "macro",
		Language:     "ar",
		Region:       "mena",
		Trust:        0.82,
		Confidence:   0.58,
		SuccessCount: 3,
	}})

	peer, ok := graph.LookupPeer("desk-geo-a", "desk-macro-a", "macro", "medium:neutral:risk_on:normal")
	if !ok || peer.Trust != 0.71 {
		t.Fatalf("expected loaded peer belief, got %+v", peer)
	}
	if peer.RelationshipHealth != 0.50 || peer.RecoveryScore != 0 {
		t.Fatalf("expected peer relationship defaults, got %+v", peer)
	}

	source, ok := graph.LookupSource("thomson_reuters", "reuters.com", "macro", "ar", "mena")
	if !ok || source.Trust != 0.82 {
		t.Fatalf("expected loaded source belief, got %+v", source)
	}
}

func TestLeadTimeBeliefObservationsAndLoad(t *testing.T) {
	graph := NewGraph()
	key := LeadTimeBeliefKey("telegram/mena", "geopolitical", "ar", "mena")

	graph.RecordLeadTimeObservation(key, 2.0)
	graph.RecordLeadTimeObservation(key, 4.0)

	lead, ok := graph.LookupLeadTime("telegram/mena", "geopolitical", "ar", "mena")
	if !ok {
		t.Fatal("expected lead-time belief to be created")
	}
	if lead.Observations != 2 {
		t.Fatalf("expected two lead-time observations, got %+v", lead)
	}
	if lead.AverageHours < 2.9 || lead.AverageHours > 3.1 {
		t.Fatalf("expected ~3h average lead time, got %.2f", lead.AverageHours)
	}

	graph2 := NewGraph()
	graph2.LoadLeadTimeBeliefs([]*model.SourceLeadTimeBelief{lead})
	loaded, ok := graph2.LookupLeadTime("telegram/mena", "geopolitical", "ar", "mena")
	if !ok || loaded.Observations != 2 {
		t.Fatalf("expected loaded lead-time belief, got %+v", loaded)
	}
}

func TestCompetenceStateGovernanceCeilings(t *testing.T) {
	graph := NewGraph()
	key := CompetenceKey("desk-a", "macro", "STK", "medium:neutral:risk_on:normal")

	graph.Load([]*model.CompetenceState{{
		Key:          key,
		DeskID:       "desk-a",
		Capability:   "macro",
		Context:      "STK",
		Regime:       "medium:neutral:risk_on:normal",
		Trust:        0.95,
		Confidence:   0.90,
		SuccessCount: 2,
	}})

	state, ok := graph.Lookup("desk-a", "macro", "STK", "medium:neutral:risk_on:normal")
	if !ok {
		t.Fatal("expected competence state to load")
	}
	if state.ValidatedOutcomes != 2 {
		t.Fatalf("expected validated outcomes to infer from observations, got %+v", state)
	}
	if state.TrustCeiling != 0.62 || state.ConfidenceCeiling != 0.45 {
		t.Fatalf("expected early-stage ceilings to apply, got %+v", state)
	}
	if state.Trust != state.TrustCeiling || state.Confidence != state.ConfidenceCeiling {
		t.Fatalf("expected trust/confidence to clamp to ceilings, got %+v", state)
	}

	graph.ApplySuccess(key, 1.0)
	state, ok = graph.Lookup("desk-a", "macro", "STK", "medium:neutral:risk_on:normal")
	if !ok {
		t.Fatal("expected competence state after success")
	}
	if state.ValidatedOutcomes != 3 {
		t.Fatalf("expected validated outcomes to increment, got %+v", state)
	}
	if state.TrustCeiling != 0.72 || state.ConfidenceCeiling != 0.58 {
		t.Fatalf("expected next ceiling band after additional validation, got %+v", state)
	}
	if state.Trust > state.TrustCeiling || state.Confidence > state.ConfidenceCeiling {
		t.Fatalf("expected state to remain within ceilings, got %+v", state)
	}
}
