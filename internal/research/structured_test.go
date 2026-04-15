package research

import (
	"strings"
	"testing"
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
