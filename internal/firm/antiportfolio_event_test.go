package firm

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestAntiPortfolioDecisionEventCarriesDecisionContext(t *testing.T) {
	thesis := &model.Thesis{
		ID:            "thesis-risk",
		OpportunityID: "opp-risk",
		DeskID:        "macro-rates-a",
		Domain:        "macro",
		Strategy:      "rates_breakout",
		Instrument: model.Instrument{
			Symbol:   "TLT",
			SecType:  "STK",
			Currency: "USD",
		},
		Direction:    model.Long,
		Status:       model.ThesisProsecuted,
		Conviction:   0.68,
		PositionSize: 0.03,
		EntryPrice:   91.25,
		Prosecution: &model.Prosecution{
			Verdict:    "survived",
			Confidence: 0.02,
		},
	}

	entry := antiPortfolioDecisionEvent("macro-rates-a", "macro", thesis, "blocked_by_risk_gate", 0.65)
	if entry.EventType != "thesis_rejected" {
		t.Fatalf("event type = %q", entry.EventType)
	}
	if entry.Severity != "info" {
		t.Fatalf("severity = %q", entry.Severity)
	}
	if entry.Metadata["stage"] != "risk" {
		t.Fatalf("stage metadata = %#v", entry.Metadata["stage"])
	}
	if entry.Metadata["thesis_id"] != "thesis-risk" || entry.Metadata["symbol"] != "TLT" {
		t.Fatalf("missing thesis context: %+v", entry.Metadata)
	}
	if entry.Metadata["min_conviction"] != 0.65 {
		t.Fatalf("missing threshold context: %+v", entry.Metadata)
	}
	if entry.Metadata["prosecution_verdict"] != "survived" {
		t.Fatalf("missing prosecution context: %+v", entry.Metadata)
	}
	if entry.Metadata["counterfactual_status"] != "not_evaluated" {
		t.Fatalf("missing counterfactual status: %+v", entry.Metadata)
	}
}

func TestRejectionStageClassifiesKnownStops(t *testing.T) {
	cases := map[string]string{
		"conviction_below_threshold":            "research",
		"killed_by_prosecutor":                  "prosecutor",
		"council_rejected":                      "council",
		"blocked_by_runtime_health":             "runtime_health",
		"blocked_by_risk_gate":                  "risk",
		"kalshi_execution_rejected":             "execution",
		"something_new_that_we_have_not_mapped": "unknown",
	}
	for reason, want := range cases {
		if got := rejectionStage(reason); got != want {
			t.Fatalf("rejectionStage(%q) = %q, want %q", reason, got, want)
		}
	}
}
