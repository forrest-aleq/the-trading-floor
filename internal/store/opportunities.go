package store

import (
	"context"
	"encoding/json"

	"github.com/hnic/trading-floor/pkg/model"
)

func (db *DB) UpsertOpportunity(ctx context.Context, opp *model.Opportunity) error {
	instruments, err := json.Marshal(opp.Instruments)
	if err != nil {
		return err
	}

	var cascadeInfo []byte
	if opp.CascadeInfo != nil {
		cascadeInfo, err = json.Marshal(opp.CascadeInfo)
		if err != nil {
			return err
		}
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO opportunities (
			id, signal_ids, instruments, direction, urgency, score, category, cascade_info, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)
		ON CONFLICT (id) DO UPDATE SET
			signal_ids = EXCLUDED.signal_ids,
			instruments = EXCLUDED.instruments,
			direction = EXCLUDED.direction,
			urgency = EXCLUDED.urgency,
			score = EXCLUDED.score,
			category = EXCLUDED.category,
			cascade_info = EXCLUDED.cascade_info`,
		opp.ID,
		opp.SignalIDs,
		instruments,
		string(opp.Direction),
		opp.Urgency,
		opp.Score,
		opp.Category,
		cascadeInfo,
		opp.CreatedAt,
	)
	return err
}
