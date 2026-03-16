package research

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestNewCouncilIncludesExtendedVoices(t *testing.T) {
	council := NewCouncil(nil)
	if len(council.archetypes) != 9 {
		t.Fatalf("expected 9 council voices, got %d", len(council.archetypes))
	}

	required := map[string]bool{
		"Fundamental":              false,
		"Contrarian":               false,
		"Macro":                    false,
		"Tail":                     false,
		"Timing":                   false,
		"Market-Implied":           false,
		"Source-Forensics":         false,
		"Execution-Microstructure": false,
		"Abstain":                  false,
	}
	for _, arch := range council.archetypes {
		if _, ok := required[arch.Name]; ok {
			required[arch.Name] = true
		}
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("expected council voice %s to be registered", name)
		}
	}
}

func TestCouncilSynthesizeHonorsWeightsAndRecommendations(t *testing.T) {
	council := NewCouncil(nil)
	thesis := &model.Thesis{
		ID:           "thesis-1",
		Conviction:   0.80,
		PositionSize: 0.04,
	}

	verdict := council.synthesize(thesis, []perspectiveResult{
		{
			name:           "Fundamental",
			view:           "numbers support the thesis",
			recommendation: model.CouncilApprove,
			conviction:     0.10,
			size:           1.10,
			weight:         1.0,
		},
		{
			name:           "Market-Implied",
			view:           "most of the surprise is already priced",
			recommendation: model.CouncilReject,
			conviction:     -0.18,
			size:           0.75,
			weight:         1.35,
		},
		{
			name:           "Abstain",
			view:           "wait for cleaner confirmation",
			recommendation: model.CouncilAbstain,
			conviction:     -0.08,
			size:           0.85,
			weight:         1.25,
		},
	})

	if verdict.Approved {
		t.Fatalf("expected weighted reject/abstain votes to fail the trade")
	}
	if len(verdict.Voices) != 3 {
		t.Fatalf("expected 3 structured council voices, got %d", len(verdict.Voices))
	}
	if verdict.WeightedVoteScore >= 0 {
		t.Fatalf("expected negative weighted vote score, got %.2f", verdict.WeightedVoteScore)
	}
	if verdict.TotalWeight <= 0 {
		t.Fatalf("expected total weight to be tracked")
	}
}
