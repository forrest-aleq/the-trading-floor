package store

import (
	"context"
	"encoding/json"
	"time"
)

type EngramRecord struct {
	ID             string
	IntentKey      string
	ContextPattern string
	Capability     string
	DeskID         string
	Layer          int
	SuccessCount   int
	FailureCount   int
	AvgReturn      float64
	Sharpe         float64
	RegimeTags     []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (db *DB) LoadEngrams(ctx context.Context) ([]*EngramRecord, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, intent_key, context_pattern, capability, COALESCE(desk_id, ''),
		       layer, success_count, failure_count, avg_return, sharpe,
		       COALESCE(regime_tags, ARRAY[]::TEXT[]), created_at, updated_at
		  FROM engrams
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*EngramRecord
	for rows.Next() {
		var record EngramRecord
		if err := rows.Scan(
			&record.ID,
			&record.IntentKey,
			&record.ContextPattern,
			&record.Capability,
			&record.DeskID,
			&record.Layer,
			&record.SuccessCount,
			&record.FailureCount,
			&record.AvgReturn,
			&record.Sharpe,
			&record.RegimeTags,
			&record.CreatedAt,
			&record.UpdatedAt,
		); err != nil {
			return nil, err
		}
		records = append(records, &record)
	}
	return records, rows.Err()
}

func (db *DB) UpsertEngram(ctx context.Context, record *EngramRecord) error {
	if record == nil {
		return nil
	}

	actionPlan, err := json.Marshal(map[string]any{
		"intent_key":      record.IntentKey,
		"context_pattern": record.ContextPattern,
		"capability":      record.Capability,
	})
	if err != nil {
		return err
	}

	_, err = db.Pool.Exec(ctx, `
		INSERT INTO engrams (
			id, intent_key, context_pattern, capability, desk_id, layer, action_plan,
			success_count, failure_count, avg_return, sharpe, regime_tags,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, NULLIF($5, ''), $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14
		)
		ON CONFLICT (id) DO UPDATE SET
			intent_key = EXCLUDED.intent_key,
			context_pattern = EXCLUDED.context_pattern,
			capability = EXCLUDED.capability,
			desk_id = EXCLUDED.desk_id,
			layer = EXCLUDED.layer,
			action_plan = EXCLUDED.action_plan,
			success_count = EXCLUDED.success_count,
			failure_count = EXCLUDED.failure_count,
			avg_return = EXCLUDED.avg_return,
			sharpe = EXCLUDED.sharpe,
			regime_tags = EXCLUDED.regime_tags,
			updated_at = EXCLUDED.updated_at
	`,
		record.ID,
		record.IntentKey,
		record.ContextPattern,
		record.Capability,
		record.DeskID,
		record.Layer,
		actionPlan,
		record.SuccessCount,
		record.FailureCount,
		record.AvgReturn,
		record.Sharpe,
		record.RegimeTags,
		record.CreatedAt,
		record.UpdatedAt,
	)
	return err
}
