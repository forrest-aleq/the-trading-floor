package store

import (
	"encoding/json"
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestBuildAntiPortfolioRecordPromotesDecisionContext(t *testing.T) {
	thesis := &model.Thesis{
		ID:            "thesis-1",
		OpportunityID: "opp-1",
		DeskID:        "kalshi-rates-a",
		Domain:        "prediction_market",
		Strategy:      "event_probability",
		Instrument: model.Instrument{
			Symbol:   "KXFED-26",
			SecType:  model.SecTypeKalshi,
			Currency: "USD",
		},
		Direction:    model.Long,
		Status:       model.ThesisProsecuted,
		Conviction:   0.61,
		Health:       0.74,
		PositionSize: 0.02,
		EntryPrice:   0.42,
		TargetPrice:  0.62,
		StopLoss:     0.24,
		Prosecution: &model.Prosecution{
			Verdict:    "weakened",
			Confidence: -0.08,
		},
		CouncilVerdict: &model.CouncilVerdict{
			Approved:          false,
			WeightedVoteScore: -0.35,
			Voices: []model.CouncilVoiceContribution{
				{Name: "execution", Recommendation: model.CouncilReject},
			},
		},
	}

	record, err := buildAntiPortfolioRecord(thesis, "council_rejected")
	if err != nil {
		t.Fatalf("build anti-portfolio record: %v", err)
	}
	if record.ThesisID != "thesis-1" || record.OpportunityID != "opp-1" || record.Domain != "prediction_market" {
		t.Fatalf("missing promoted identifiers: %+v", record)
	}
	if record.StatusAtRejection != string(model.ThesisProsecuted) {
		t.Fatalf("status at rejection = %q", record.StatusAtRejection)
	}
	if record.ProsecutionVerdict != "weakened" || record.ProsecutionConfidence == nil || *record.ProsecutionConfidence != -0.08 {
		t.Fatalf("missing prosecution context: %+v", record)
	}
	if record.CouncilApproved == nil || *record.CouncilApproved || record.CouncilVoiceCount != 1 {
		t.Fatalf("missing council context: %+v", record)
	}
	if record.CounterfactualStatus != "not_evaluated" {
		t.Fatalf("counterfactual status = %q", record.CounterfactualStatus)
	}

	var metadata map[string]any
	if err := json.Unmarshal(record.Metadata, &metadata); err != nil {
		t.Fatalf("metadata json: %v", err)
	}
	if metadata["rejection_reason"] != "council_rejected" {
		t.Fatalf("metadata missing rejection reason: %+v", metadata)
	}
	if metadata["counterfactual_reason"] != "counterfactual_pnl_not_evaluated" {
		t.Fatalf("metadata missing explicit counterfactual state: %+v", metadata)
	}
}
