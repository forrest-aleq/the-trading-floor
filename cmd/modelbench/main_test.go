package main

import "testing"

func TestScoreJSONDecision(t *testing.T) {
	result := benchResult{
		Mode:     "json",
		Response: `{"tradeable":true,"side":"yes","limit_price_cents":45,"max_dollars":1.0,"conviction":0.82,"category":"prediction_market","rationale":"proxy edge"}`,
	}
	scoreResult(&result, benchCase{
		ExpectedTrade:    true,
		ExpectedSide:     "yes",
		ExpectedCategory: "prediction_market",
		MaxDollars:       1,
	})

	if !result.StructuredOK || !result.DecisionOK || !result.SideOK || !result.CategoryOK || !result.RiskOK {
		t.Fatalf("expected perfect JSON score flags, got %+v", result)
	}
	if result.Score != 100 {
		t.Fatalf("score = %d, want 100", result.Score)
	}
}

func TestScoreJSONDecisionBlocksOversizedRisk(t *testing.T) {
	result := benchResult{
		Mode:     "json",
		Response: `{"tradeable":true,"side":"yes","limit_price_cents":45,"max_dollars":5.0,"conviction":0.82,"category":"prediction_market","rationale":"proxy edge"}`,
	}
	scoreResult(&result, benchCase{
		ExpectedTrade:    true,
		ExpectedSide:     "yes",
		ExpectedCategory: "prediction_market",
		MaxDollars:       1,
	})

	if result.RiskOK {
		t.Fatalf("expected oversized risk to fail, got %+v", result)
	}
	if result.Score != 95 {
		t.Fatalf("score = %d, want 95", result.Score)
	}
}

func TestScoreJSONDecisionRequiresPromptedFields(t *testing.T) {
	result := benchResult{
		Mode:     "json",
		Response: `{"tradeable":false}`,
	}
	scoreResult(&result, benchCase{
		ExpectedTrade:    false,
		ExpectedSide:     "none",
		ExpectedCategory: "prediction_market",
		MaxDollars:       1,
	})

	if result.StructuredOK {
		t.Fatalf("expected missing prompted fields to fail structure, got %+v", result)
	}
	if result.Score != 0 || result.DecisionOK || result.SideOK || result.CategoryOK || result.RiskOK {
		t.Fatalf("expected incomplete JSON to leave score flags unset, got %+v", result)
	}
}

func TestScoreJSONNoTradeRequiresZeroRisk(t *testing.T) {
	result := benchResult{
		Mode:     "json",
		Response: `{"tradeable":false,"side":"none","limit_price_cents":0,"max_dollars":1.0,"conviction":0.0,"category":"prediction_market","rationale":"no edge"}`,
	}
	scoreResult(&result, benchCase{
		ExpectedTrade:    false,
		ExpectedSide:     "none",
		ExpectedCategory: "prediction_market",
		MaxDollars:       1,
	})

	if !result.StructuredOK || !result.DecisionOK || !result.SideOK || !result.CategoryOK {
		t.Fatalf("expected no-trade decision fields to match, got %+v", result)
	}
	if result.RiskOK {
		t.Fatalf("expected no-trade positive risk to fail, got %+v", result)
	}
	if result.Score != 95 {
		t.Fatalf("score = %d, want 95", result.Score)
	}
}

func TestParseThoughtDecision(t *testing.T) {
	decision, ok := parseThoughtDecision(`reasoning here
FINAL_DECISION
tradeable: false
score: 12
instruments: none
direction: none
urgency: 0.0
category: prediction_market
max_dollars: 0
reasoning: no edge
END_FINAL_DECISION`)
	if !ok {
		t.Fatal("expected thought decision to parse")
	}
	if decision.Tradeable || decision.Direction != "none" || decision.Category != "prediction_market" {
		t.Fatalf("unexpected parsed decision: %+v", decision)
	}
	if decision.Score != 12 || decision.Instruments != "none" || decision.Urgency != 0 || decision.MaxDollars != 0 || decision.Reasoning != "no edge" {
		t.Fatalf("unexpected terminal fields: %+v", decision)
	}
}

func TestParseThoughtDecisionRequiresFullTerminalSchema(t *testing.T) {
	_, ok := parseThoughtDecision(`reasoning here
FINAL_DECISION
tradeable: false
direction: none
category: prediction_market
max_dollars: 0
reasoning: no edge
END_FINAL_DECISION`)
	if ok {
		t.Fatal("expected missing score, instruments, and urgency fields to fail")
	}
}
