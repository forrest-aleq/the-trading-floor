package store

import (
	"context"

	"github.com/hnic/trading-floor/pkg/model"
)

func (db *DB) LoadCompetenceStates(ctx context.Context) ([]*model.CompetenceState, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT key, desk_id, capability, context, regime, trust, confidence,
		       success_count, failure_count, total_pnl, sharpe, autonomy_mode,
		       is_backfilled, updated_at
		  FROM competence_states
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []*model.CompetenceState
	for rows.Next() {
		var state model.CompetenceState
		if err := rows.Scan(
			&state.Key,
			&state.DeskID,
			&state.Capability,
			&state.Context,
			&state.Regime,
			&state.Trust,
			&state.Confidence,
			&state.SuccessCount,
			&state.FailureCount,
			&state.TotalPnL,
			&state.Sharpe,
			&state.Autonomy,
			&state.IsBackfilled,
			&state.UpdatedAt,
		); err != nil {
			return nil, err
		}
		states = append(states, &state)
	}
	return states, rows.Err()
}

func (db *DB) UpsertCompetenceState(ctx context.Context, state *model.CompetenceState) error {
	if state == nil {
		return nil
	}

	_, err := db.Pool.Exec(ctx, `
		INSERT INTO competence_states (
			key, desk_id, capability, context, regime, trust, confidence,
			success_count, failure_count, total_pnl, sharpe, autonomy_mode,
			is_backfilled, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14
		)
		ON CONFLICT (key) DO UPDATE SET
			trust = EXCLUDED.trust,
			confidence = EXCLUDED.confidence,
			success_count = EXCLUDED.success_count,
			failure_count = EXCLUDED.failure_count,
			total_pnl = EXCLUDED.total_pnl,
			sharpe = EXCLUDED.sharpe,
			autonomy_mode = EXCLUDED.autonomy_mode,
			is_backfilled = EXCLUDED.is_backfilled,
			updated_at = EXCLUDED.updated_at
	`,
		state.Key,
		state.DeskID,
		state.Capability,
		state.Context,
		state.Regime,
		state.Trust,
		state.Confidence,
		state.SuccessCount,
		state.FailureCount,
		state.TotalPnL,
		state.Sharpe,
		string(state.Autonomy),
		state.IsBackfilled,
		state.UpdatedAt,
	)
	return err
}
