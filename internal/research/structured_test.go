package research

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAddTerminalJSONContractAddsRequiredSentinels(t *testing.T) {
	got := addTerminalJSONContract("system prompt")
	if !strings.Contains(got, terminalJSONStart) || !strings.Contains(got, terminalJSONEnd) {
		t.Fatalf("expected terminal JSON sentinels in prompt, got %q", got)
	}
}

func TestExtractStructuredJSONFallsBackToTerminalBlock(t *testing.T) {
	raw := "Thinking Process:\n1. Evaluate evidence.\nFINAL_JSON\n{\"value\": 1}\nEND_FINAL_JSON"
	got, err := extractStructuredJSON(raw)
	if err != nil {
		t.Fatalf("expected terminal JSON extraction, got %v", err)
	}
	if got != "{\"value\": 1}" {
		t.Fatalf("unexpected cleaned JSON %q", got)
	}
}

func TestTruncateForCompilerPrefersTerminalJSONBlock(t *testing.T) {
	raw := "long reasoning prefix that should be dropped\nFINAL_JSON\n{\"value\": 1}\nEND_FINAL_JSON"
	got := truncateForCompiler(raw, len("{\"value\": 1}")+8)
	if strings.Contains(got, "long reasoning prefix") {
		t.Fatalf("expected compiler truncation to drop freeform prefix, got %q", got)
	}
	if !strings.Contains(got, "{\"value\": 1}") {
		t.Fatalf("expected compiler truncation to retain terminal JSON block, got %q", got)
	}
}

func TestStructuredBudgetTimeoutLeavesRoomForFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	got := structuredBudgetTimeout(ctx, 30*time.Second, 0.5)
	if got <= 0 {
		t.Fatalf("expected positive timeout, got %s", got)
	}
	if got > 6*time.Second {
		t.Fatalf("expected timeout to respect remaining budget, got %s", got)
	}
}

func TestHasStructuredBudgetRespectsExpiredContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)

	if hasStructuredBudget(ctx, time.Second) {
		t.Fatal("expected expired context to report no remaining structured budget")
	}
}
