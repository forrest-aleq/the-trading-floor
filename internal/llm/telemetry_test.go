package llm

import (
	"context"
	"testing"
	"time"
)

func TestWithUsageContextMergesStageWithoutDroppingDesk(t *testing.T) {
	ctx := WithUsageContext(context.Background(), UsageContext{
		DeskID:   "corp-earnings-a",
		Domain:   "corporate",
		SignalID: "sig-1",
	})
	ctx = WithUsageContext(ctx, UsageContext{Stage: "research", ThesisID: "thesis-1"})

	got, ok := UsageContextFrom(ctx)
	if !ok {
		t.Fatal("expected usage context")
	}
	if got.DeskID != "corp-earnings-a" || got.Domain != "corporate" || got.SignalID != "sig-1" {
		t.Fatalf("context identity was not preserved: %+v", got)
	}
	if got.Stage != "research" || got.ThesisID != "thesis-1" {
		t.Fatalf("stage context was not applied: %+v", got)
	}
}

func TestRecordTokenUsageAggregatesByDeskStageAndModel(t *testing.T) {
	resetTokenUsageForTest()

	ctx := WithUsageContext(context.Background(), UsageContext{
		DeskID: "kalshi-macro-a",
		Domain: "prediction_market",
		Stage:  "scanner",
	})
	recordTokenUsage(ctx, Request{Tier: TierSpeed, Model: "model-a"}, &Response{
		Model:        "model-a",
		InputTokens:  11,
		OutputTokens: 7,
	}, time.Millisecond)

	got := TokenUsageStats()
	if got.Attempts != 1 || got.Calls != 1 || got.Errors != 0 || got.InputTokens != 11 || got.OutputTokens != 7 || got.TotalTokens != 18 {
		t.Fatalf("unexpected aggregate usage: %+v", got)
	}
	if got.DeskTokens["kalshi-macro-a"] != 18 || got.DeskCalls["kalshi-macro-a"] != 1 || got.DeskAttempts["kalshi-macro-a"] != 1 {
		t.Fatalf("unexpected desk usage: %+v", got)
	}
	if got.StageTokens["scanner"] != 18 || got.StageCalls["scanner"] != 1 || got.StageAttempts["scanner"] != 1 {
		t.Fatalf("unexpected stage usage: %+v", got)
	}
	if got.ModelTokens["model-a"] != 18 || got.ModelCalls["model-a"] != 1 || got.ModelAttempts["model-a"] != 1 {
		t.Fatalf("unexpected model usage: %+v", got)
	}
}

func TestRecordTokenFailureAggregatesErrorsAndEstimatedInput(t *testing.T) {
	resetTokenUsageForTest()

	ctx := WithUsageContext(context.Background(), UsageContext{
		DeskID: "corp-earnings-a",
		Domain: "corporate",
		Stage:  "research",
	})
	recordTokenFailure(ctx, Request{
		Tier:  TierAnalysis,
		Model: "model-b",
		Messages: []Message{
			{Role: RoleSystem, Content: "system prompt"},
			{Role: RoleUser, Content: "12345678"},
		},
	}, "model-b", context.Canceled, time.Millisecond)

	got := TokenUsageStats()
	if got.Attempts != 1 || got.Calls != 0 || got.Errors != 1 {
		t.Fatalf("unexpected failure aggregate: %+v", got)
	}
	if got.EstimatedInputTokens != 6 {
		t.Fatalf("estimated input tokens = %d, want 6", got.EstimatedInputTokens)
	}
	if got.DeskAttempts["corp-earnings-a"] != 1 || got.DeskErrors["corp-earnings-a"] != 1 {
		t.Fatalf("unexpected desk failure usage: %+v", got)
	}
	if got.StageAttempts["research"] != 1 || got.StageErrors["research"] != 1 {
		t.Fatalf("unexpected stage failure usage: %+v", got)
	}
	if got.ModelAttempts["model-b"] != 1 || got.ModelErrors["model-b"] != 1 {
		t.Fatalf("unexpected model failure usage: %+v", got)
	}
}
