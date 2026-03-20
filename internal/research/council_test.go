package research

import (
	"context"
	"testing"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

type councilStubClient struct {
	requests []llm.Request
}

func (s *councilStubClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: validCouncilPerspectiveJSON(),
		Model:   "stub",
	}, nil
}

type councilCompilerFallbackClient struct {
	requests []llm.Request
}

func (s *councilCompilerFallbackClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &llm.Response{
			Content: "Thinking Process:\n1. Timing is acceptable.\n2. Liquidity is fine.\n3. The model forgot to emit JSON.",
			Model:   "critical",
		}, nil
	default:
		return &llm.Response{
			Content: validCouncilPerspectiveJSON(),
			Model:   "compiler",
		}, nil
	}
}

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

func TestCouncilPerspectiveUsesThoughtModeForQwen(t *testing.T) {
	t.Setenv("COUNCIL_MODEL", "qwen/qwen3.5-35b-a3b")

	client := &councilStubClient{}
	council := NewCouncil(llm.NewRouter(client, client, client))

	cleaned, err := council.requestPerspectiveJSON(context.Background(), "Macro", "macro system prompt", "thesis prompt")
	if err != nil {
		t.Fatalf("expected structured perspective, got %v", err)
	}
	if cleaned == "" {
		t.Fatal("expected cleaned JSON")
	}
	if len(client.requests) == 0 {
		t.Fatal("expected council request")
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected Qwen council request to avoid strict JSON mode")
	}
}

func TestCouncilPerspectiveCompilerFallbackRecoversStructuredJSON(t *testing.T) {
	t.Setenv("COUNCIL_MODEL", "qwen/qwen3.5-35b-a3b")
	t.Setenv("COUNCIL_COMPILER_MODEL", "gemma-the-writer-mighty-sword-9b")

	client := &councilCompilerFallbackClient{}
	council := NewCouncil(llm.NewRouter(client, client, client))

	cleaned, err := council.requestPerspectiveJSON(context.Background(), "Macro", "macro system prompt", "thesis prompt")
	if err != nil {
		t.Fatalf("expected compiler fallback to recover perspective, got %v", err)
	}
	if cleaned == "" {
		t.Fatal("expected cleaned JSON from compiler fallback")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected council call plus compiler call, got %d", got)
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected initial council call to avoid strict JSON mode")
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected council compiler request to use strict JSON mode")
	}
	if client.requests[1].Model != "gemma-the-writer-mighty-sword-9b" {
		t.Fatalf("unexpected compiler model %q", client.requests[1].Model)
	}
}

func validCouncilPerspectiveJSON() string {
	return `{
  "perspective": "timing and liquidity are acceptable",
  "recommendation": "approve",
  "conviction_adjustment": 0.05,
  "size_adjustment": 1.05,
  "reasoning": "the structure is executable with manageable slippage"
}`
}
