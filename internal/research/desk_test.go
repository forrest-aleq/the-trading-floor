package research

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type researchStubClient struct {
	requests []llm.Request
}

func (s *researchStubClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: validResearchJSON(),
		Model:   "stub",
	}, nil
}

type researchCompilerFallbackClient struct {
	requests []llm.Request
}

func (s *researchCompilerFallbackClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &llm.Response{
			Content: "Thinking Process:\n1. Strong event setup.\n2. Clear catalyst.\n3. The model forgot to emit JSON.",
			Model:   "analysis",
		}, nil
	default:
		return &llm.Response{
			Content: validResearchJSON(),
			Model:   "compiler",
		}, nil
	}
}

type researchTerminalBlockClient struct {
	requests []llm.Request
}

func (s *researchTerminalBlockClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: "Thinking Process:\n1. Strong event setup.\n2. Clear catalyst.\nFINAL_JSON\n" + validResearchJSON() + "\nEND_FINAL_JSON",
		Model:   "analysis",
	}, nil
}

func TestInvestigateUsesThoughtModeForQwenResearch(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")

	client := &researchStubClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected thesis, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected non-nil thesis")
	}
	if len(client.requests) == 0 {
		t.Fatal("expected research request")
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected Qwen research request to avoid strict JSON mode")
	}
	if got := client.requests[0].Messages[0].Content; got == researchPrompt {
		t.Fatal("expected thought-friendly research prompt prefix")
	}
	if got := client.requests[0].Messages[0].Content; !containsTerminalContract(got) {
		t.Fatalf("expected terminal JSON contract in research prompt, got %q", got)
	}
}

func TestInvestigateCompilerFallbackRecoversStructuredThesis(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")
	t.Setenv("RESEARCH_COMPILER_MODEL", "gemma-the-writer-mighty-sword-9b")

	client := &researchCompilerFallbackClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected compiler fallback to recover thesis, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis from compiler fallback")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected analysis call plus compiler call, got %d", got)
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected initial research call to avoid strict JSON mode")
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected compiler request to use strict JSON mode")
	}
	if client.requests[1].Model != "gemma-the-writer-mighty-sword-9b" {
		t.Fatalf("unexpected compiler model %q", client.requests[1].Model)
	}
}

func TestInvestigateAcceptsTerminalJSONBlockWithoutCompilerFallback(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")

	client := &researchTerminalBlockClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected terminal JSON block to parse, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis")
	}
	if got := len(client.requests); got != 1 {
		t.Fatalf("expected only one research call, got %d", got)
	}
}

func testOpportunity() *model.Opportunity {
	return &model.Opportunity{
		ID: "opp-1",
		Instruments: []model.Instrument{{
			Symbol:   "TLT",
			SecType:  "STK",
			Currency: "USD",
			Exchange: "SMART",
		}},
		Direction: model.Long,
		Urgency:   0.8,
		Score:     78,
		Category:  "macro",
		SignalIDs: []string{"sig-1"},
		CreatedAt: time.Now(),
	}
}

func testSignal() signal.Signal {
	return signal.Signal{
		ID:         "sig-1",
		Source:     "fed-speeches",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.8,
		Translated: "Federal Reserve speech signaled a more hawkish balance of risks.",
	}
}

func validResearchJSON() string {
	return fmt.Sprintf(`{
  "structure": "single",
  "instrument": {"symbol": "TLT", "sec_type": "STK", "currency": "USD", "exchange": "SMART", "expiry": "", "strike": 0, "right": ""},
  "legs": [],
  "direction": "long",
  "entry_price": 90.5,
  "target_price": 96.0,
  "stop_loss": 88.0,
  "conviction": 0.74,
  "time_horizon_hours": 48,
  "position_size_pct": 0.01,
  "strategy": "macro",
  "surprise_assessment": {
    "truth_score": 0.8,
    "novelty_score": 0.7,
    "priced_in_score": 0.3,
    "reaction_gap_score": 0.6,
    "unmoved_asset_score": 0.5,
    "summary": "rates repricing is incomplete"
  },
  "evidence": ["hawkish policy rhetoric", "duration-sensitive setup", "clean rate proxy"],
  "counter_args": ["speech may be ignored", "positioning could already be crowded"],
  "kill_rules": [{"condition": "price_below_stop", "threshold": 88.0, "action": "close"}],
  "reasoning": "hawkish repricing favors duration rebound after overshoot"
}`)
}

func containsTerminalContract(prompt string) bool {
	return strings.Contains(prompt, terminalJSONStart) && strings.Contains(prompt, terminalJSONEnd)
}
