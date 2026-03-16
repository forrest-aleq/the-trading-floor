package store

import (
	"context"
	"encoding/json"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/jackc/pgx/v5"
)

func (db *DB) UpsertThesis(ctx context.Context, thesis *model.Thesis) error {
	instrument, err := json.Marshal(thesis.Instrument)
	if err != nil {
		return err
	}
	legs, err := json.Marshal(thesis.Legs)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(thesis.Evidence)
	if err != nil {
		return err
	}
	counterArgs, err := json.Marshal(thesis.CounterArgs)
	if err != nil {
		return err
	}
	killRules, err := json.Marshal(thesis.KillRules)
	if err != nil {
		return err
	}
	var marketContext []byte
	if thesis.MarketContext != nil {
		marketContext, err = json.Marshal(thesis.MarketContext)
		if err != nil {
			return err
		}
	}
	var surpriseAssessment []byte
	if thesis.SurpriseAssessment != nil {
		surpriseAssessment, err = json.Marshal(thesis.SurpriseAssessment)
		if err != nil {
			return err
		}
	}

	var prosecution []byte
	if thesis.Prosecution != nil {
		prosecution, err = json.Marshal(thesis.Prosecution)
		if err != nil {
			return err
		}
	}

	var councilVerdict []byte
	if thesis.CouncilVerdict != nil {
		councilVerdict, err = json.Marshal(thesis.CouncilVerdict)
		if err != nil {
			return err
		}
	}

	var outcome []byte
	if thesis.Outcome != nil {
		outcome, err = json.Marshal(thesis.Outcome)
		if err != nil {
			return err
		}
	}

	horizon := ""
	if thesis.TimeHorizon > 0 {
		horizon = thesis.TimeHorizon.String()
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO theses (
			id, opportunity_id, desk_id, strategy, structure, instrument, legs, direction, conviction, health,
			evidence, counter_args, entry_price, target_price, stop_loss, position_size,
			time_horizon, kill_rules, status, autonomy_mode, scan_territory, execution_territory,
			competence_key, competence_trust, competence_confidence, market_context, surprise_assessment,
			prosecution, council_verdict, outcome,
			created_at, resolved_at, domain
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16,
			NULLIF($17, '')::interval, $18, $19, $20, $21, $22,
			$23, $24, $25, $26, $27,
			$28, $29, $30,
			$31, $32, $33
		)
		ON CONFLICT (id) DO UPDATE SET
			opportunity_id = EXCLUDED.opportunity_id,
			strategy = EXCLUDED.strategy,
			structure = EXCLUDED.structure,
			instrument = EXCLUDED.instrument,
			legs = EXCLUDED.legs,
			direction = EXCLUDED.direction,
			conviction = EXCLUDED.conviction,
			health = EXCLUDED.health,
			evidence = EXCLUDED.evidence,
			counter_args = EXCLUDED.counter_args,
			entry_price = EXCLUDED.entry_price,
			target_price = EXCLUDED.target_price,
			stop_loss = EXCLUDED.stop_loss,
			position_size = EXCLUDED.position_size,
			time_horizon = EXCLUDED.time_horizon,
			kill_rules = EXCLUDED.kill_rules,
			status = EXCLUDED.status,
			autonomy_mode = EXCLUDED.autonomy_mode,
			scan_territory = EXCLUDED.scan_territory,
			execution_territory = EXCLUDED.execution_territory,
			competence_key = EXCLUDED.competence_key,
			competence_trust = EXCLUDED.competence_trust,
			competence_confidence = EXCLUDED.competence_confidence,
			market_context = EXCLUDED.market_context,
			surprise_assessment = EXCLUDED.surprise_assessment,
			prosecution = EXCLUDED.prosecution,
			council_verdict = EXCLUDED.council_verdict,
			outcome = EXCLUDED.outcome,
			resolved_at = EXCLUDED.resolved_at,
			domain = EXCLUDED.domain`,
		thesis.ID,
		thesis.OpportunityID,
		thesis.DeskID,
		thesis.Strategy,
		thesis.Structure,
		instrument,
		legs,
		string(thesis.Direction),
		thesis.Conviction,
		thesis.Health,
		evidence,
		counterArgs,
		thesis.EntryPrice,
		thesis.TargetPrice,
		thesis.StopLoss,
		thesis.PositionSize,
		horizon,
		killRules,
		string(thesis.Status),
		string(thesis.AutonomyMode),
		thesis.ScanTerritory,
		thesis.ExecutionTerritory,
		thesis.CompetenceKey,
		thesis.CompetenceTrust,
		thesis.CompetenceConfidence,
		marketContext,
		surpriseAssessment,
		prosecution,
		councilVerdict,
		outcome,
		thesis.CreatedAt,
		thesis.ResolvedAt,
		thesis.Domain,
	)
	return err
}

func (db *DB) GetThesis(ctx context.Context, id string) (*model.Thesis, error) {
	row := db.Pool.QueryRow(ctx,
		`SELECT instrument, direction, strategy, conviction, health, evidence, counter_args,
		        structure, legs,
		        entry_price, target_price, stop_loss, position_size, prosecution, status,
		        autonomy_mode, scan_territory, execution_territory, competence_key,
		        competence_trust, competence_confidence, market_context, surprise_assessment, domain
		   FROM theses
		  WHERE id = $1`,
		id,
	)

	var thesis model.Thesis
	var instrument []byte
	var legs []byte
	var evidence []byte
	var counterArgs []byte
	var prosecution []byte
	var marketContext []byte
	var surpriseAssessment []byte
	var direction string
	var status string
	var autonomyMode string

	err := row.Scan(
		&instrument,
		&direction,
		&thesis.Strategy,
		&thesis.Conviction,
		&thesis.Health,
		&evidence,
		&counterArgs,
		&thesis.Structure,
		&legs,
		&thesis.EntryPrice,
		&thesis.TargetPrice,
		&thesis.StopLoss,
		&thesis.PositionSize,
		&prosecution,
		&status,
		&autonomyMode,
		&thesis.ScanTerritory,
		&thesis.ExecutionTerritory,
		&thesis.CompetenceKey,
		&thesis.CompetenceTrust,
		&thesis.CompetenceConfidence,
		&marketContext,
		&surpriseAssessment,
		&thesis.Domain,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	thesis.ID = id
	thesis.Direction = model.TradeDirection(direction)
	thesis.Status = model.ThesisStatus(status)
	thesis.AutonomyMode = model.AutonomyMode(autonomyMode)

	if len(instrument) > 0 {
		if err := json.Unmarshal(instrument, &thesis.Instrument); err != nil {
			return nil, err
		}
	}
	if len(legs) > 0 {
		if err := json.Unmarshal(legs, &thesis.Legs); err != nil {
			return nil, err
		}
	}
	if len(evidence) > 0 {
		if err := json.Unmarshal(evidence, &thesis.Evidence); err != nil {
			return nil, err
		}
	}
	if len(counterArgs) > 0 {
		if err := json.Unmarshal(counterArgs, &thesis.CounterArgs); err != nil {
			return nil, err
		}
	}
	if len(prosecution) > 0 {
		if err := json.Unmarshal(prosecution, &thesis.Prosecution); err != nil {
			return nil, err
		}
	}
	if len(marketContext) > 0 {
		if err := json.Unmarshal(marketContext, &thesis.MarketContext); err != nil {
			return nil, err
		}
	}
	if len(surpriseAssessment) > 0 {
		if err := json.Unmarshal(surpriseAssessment, &thesis.SurpriseAssessment); err != nil {
			return nil, err
		}
	}

	return &thesis, nil
}
