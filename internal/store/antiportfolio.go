package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

type antiPortfolioRecord struct {
	Snapshot                 []byte
	Instrument               []byte
	Metadata                 []byte
	ThesisID                 string
	OpportunityID            string
	Domain                   string
	StatusAtRejection        string
	Conviction               float64
	PositionSize             float64
	EntryPrice               float64
	TargetPrice              float64
	StopLoss                 float64
	ProsecutionVerdict       string
	ProsecutionConfidence    *float64
	CouncilApproved          *bool
	CouncilWeightedVoteScore *float64
	CouncilVoiceCount        int
	CounterfactualStatus     string
}

func (db *DB) InsertAntiPortfolio(ctx context.Context, thesis *model.Thesis, reason string) error {
	record, err := buildAntiPortfolioRecord(thesis, reason)
	if err != nil {
		return err
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO anti_portfolio (
			thesis_snapshot, rejection_reason, desk_id, strategy, instrument, direction,
			would_have_entry, created_at,
			thesis_id, opportunity_id, domain, status_at_rejection,
			conviction, position_size, entry_price, target_price, stop_loss,
			prosecution_verdict, prosecution_confidence,
			council_approved, council_weighted_vote_score, council_voice_count,
			counterfactual_status, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8,
			$9, $10, $11, $12,
			$13, $14, $15, $16, $17,
			NULLIF($18, ''), $19,
			$20, $21, $22,
			$23, $24
		)
		ON CONFLICT DO NOTHING`,
		record.Snapshot,
		reason,
		thesis.DeskID,
		thesis.Strategy,
		record.Instrument,
		string(thesis.Direction),
		thesis.EntryPrice,
		time.Now(),
		record.ThesisID,
		record.OpportunityID,
		record.Domain,
		record.StatusAtRejection,
		record.Conviction,
		record.PositionSize,
		record.EntryPrice,
		record.TargetPrice,
		record.StopLoss,
		record.ProsecutionVerdict,
		nullableFloat(record.ProsecutionConfidence),
		nullableBool(record.CouncilApproved),
		nullableFloat(record.CouncilWeightedVoteScore),
		record.CouncilVoiceCount,
		record.CounterfactualStatus,
		record.Metadata,
	)
	return err
}

func buildAntiPortfolioRecord(thesis *model.Thesis, reason string) (antiPortfolioRecord, error) {
	snapshot, err := json.Marshal(thesis)
	if err != nil {
		return antiPortfolioRecord{}, err
	}
	instrument, err := json.Marshal(thesis.Instrument)
	if err != nil {
		return antiPortfolioRecord{}, err
	}
	metadata, err := json.Marshal(antiPortfolioMetadata(thesis, reason))
	if err != nil {
		return antiPortfolioRecord{}, err
	}

	record := antiPortfolioRecord{
		Snapshot:             snapshot,
		Instrument:           instrument,
		Metadata:             metadata,
		ThesisID:             thesis.ID,
		OpportunityID:        thesis.OpportunityID,
		Domain:               thesis.Domain,
		StatusAtRejection:    string(thesis.Status),
		Conviction:           thesis.Conviction,
		PositionSize:         thesis.PositionSize,
		EntryPrice:           thesis.EntryPrice,
		TargetPrice:          thesis.TargetPrice,
		StopLoss:             thesis.StopLoss,
		ProsecutionVerdict:   "",
		CouncilVoiceCount:    0,
		CounterfactualStatus: "not_evaluated",
	}
	if thesis.Prosecution != nil {
		record.ProsecutionVerdict = thesis.Prosecution.Verdict
		record.ProsecutionConfidence = &thesis.Prosecution.Confidence
	}
	if thesis.CouncilVerdict != nil {
		record.CouncilApproved = &thesis.CouncilVerdict.Approved
		record.CouncilWeightedVoteScore = &thesis.CouncilVerdict.WeightedVoteScore
		record.CouncilVoiceCount = len(thesis.CouncilVerdict.Voices)
	}
	return record, nil
}

func antiPortfolioMetadata(thesis *model.Thesis, reason string) map[string]any {
	metadata := map[string]any{
		"rejection_reason":      reason,
		"counterfactual_status": "not_evaluated",
		"counterfactual_reason": "counterfactual_pnl_not_evaluated",
		"thesis_id":             thesis.ID,
		"opportunity_id":        thesis.OpportunityID,
		"desk_id":               thesis.DeskID,
		"domain":                thesis.Domain,
		"strategy":              thesis.Strategy,
		"status_at_rejection":   string(thesis.Status),
		"symbol":                thesis.Instrument.Symbol,
		"sec_type":              thesis.Instrument.SecType,
		"direction":             string(thesis.Direction),
		"conviction":            thesis.Conviction,
		"health":                thesis.Health,
		"position_size":         thesis.PositionSize,
		"entry_price":           thesis.EntryPrice,
		"target_price":          thesis.TargetPrice,
		"stop_loss":             thesis.StopLoss,
		"evidence_count":        len(thesis.Evidence),
		"counter_args":          thesis.CounterArgs,
	}
	if thesis.AutonomyMode != "" {
		metadata["autonomy_mode"] = thesis.AutonomyMode
		metadata["scan_territory"] = thesis.ScanTerritory
		metadata["execution_territory"] = thesis.ExecutionTerritory
		metadata["competence_key"] = thesis.CompetenceKey
		metadata["competence_trust"] = thesis.CompetenceTrust
		metadata["competence_confidence"] = thesis.CompetenceConfidence
	}
	if thesis.Prosecution != nil {
		metadata["prosecution"] = thesis.Prosecution
		metadata["prosecution_verdict"] = thesis.Prosecution.Verdict
		metadata["prosecution_confidence"] = thesis.Prosecution.Confidence
	}
	if thesis.CouncilVerdict != nil {
		metadata["council_verdict"] = thesis.CouncilVerdict
		metadata["council_approved"] = thesis.CouncilVerdict.Approved
		metadata["council_weighted_vote_score"] = thesis.CouncilVerdict.WeightedVoteScore
		metadata["council_voice_count"] = len(thesis.CouncilVerdict.Voices)
	}
	if thesis.QuantMetrics != nil {
		metadata["quant_metrics"] = thesis.QuantMetrics
	}
	if thesis.EvidenceMeta != nil {
		metadata["evidence_meta"] = thesis.EvidenceMeta
	}
	return metadata
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}
