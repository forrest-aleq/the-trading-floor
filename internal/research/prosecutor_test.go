package research

import (
	"context"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

type prosecutorStubClient struct {
	requests []llm.Request
}

func (s *prosecutorStubClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: validProsecutionJSON(),
		Model:   "stub",
	}, nil
}

type prosecutorCompilerFallbackClient struct {
	requests []llm.Request
}

func (s *prosecutorCompilerFallbackClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &llm.Response{
			Content: "Thinking Process:\n1. Crowded setup.\n2. Thin evidence.\n3. The model forgot to emit JSON.",
			Model:   "critical",
		}, nil
	default:
		return &llm.Response{
			Content: validProsecutionJSON(),
			Model:   "compiler",
		}, nil
	}
}

func TestProsecutorUsesThoughtModeForQwen(t *testing.T) {
	t.Setenv("PROSECUTION_MODEL", "qwen/qwen3.5-35b-a3b")

	client := &prosecutorStubClient{}
	prosecutor := NewProsecutor(llm.NewRouter(client, client, client))

	result := prosecutor.Challenge(context.Background(), structuredTestThesis())
	if result == nil {
		t.Fatal("expected prosecution result")
	}
	if len(client.requests) == 0 {
		t.Fatal("expected prosecution request")
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected Qwen prosecution request to avoid strict JSON mode")
	}
}

func TestProsecutorCompilerFallbackRecoversStructuredVerdict(t *testing.T) {
	t.Setenv("PROSECUTION_MODEL", "qwen/qwen3.5-35b-a3b")
	t.Setenv("PROSECUTION_COMPILER_MODEL", "gemma-the-writer-mighty-sword-9b")

	client := &prosecutorCompilerFallbackClient{}
	prosecutor := NewProsecutor(llm.NewRouter(client, client, client))

	result := prosecutor.Challenge(context.Background(), structuredTestThesis())
	if result == nil {
		t.Fatal("expected prosecution result")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected critical call plus compiler call, got %d", got)
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected initial prosecution call to avoid strict JSON mode")
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected compiler request to use strict JSON mode")
	}
	if client.requests[1].Model != "gemma-the-writer-mighty-sword-9b" {
		t.Fatalf("unexpected compiler model %q", client.requests[1].Model)
	}
}

func structuredTestThesis() *model.Thesis {
	return &model.Thesis{
		ID: "thesis-structured",
		Instrument: model.Instrument{
			Symbol:   "AAPL",
			SecType:  "STK",
			Currency: "USD",
			Exchange: "SMART",
		},
		Direction:    model.Long,
		Strategy:     "event",
		Conviction:   0.78,
		EntryPrice:   100,
		TargetPrice:  110,
		StopLoss:     95,
		TimeHorizon:  48 * time.Hour,
		PositionSize: 0.01,
		Evidence: []model.Evidence{
			{Content: "earnings beat", Weight: 0.9},
			{Content: "guide raised", Weight: 0.8},
		},
		CounterArgs: []string{"already partially priced"},
		QuantMetrics: &model.QuantMetrics{
			Method:         "single",
			DefinedRisk:    true,
			MaxLoss:        5,
			MaxGain:        10,
			RewardToRisk:   2,
			MarginEstimate: 100,
		},
	}
}

func validProsecutionJSON() string {
	return `{
  "verdict": "survived",
  "bear_args": ["crowded", "timing risk"],
  "missing_data": ["flow positioning"],
  "historical_analogues": ["prior earnings squeeze"],
  "crowded_score": 0.35,
  "confidence_adjustment": -0.05,
  "reasoning": "the thesis survives but needs caution"
}`
}
