package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func (db *DB) InsertAntiPortfolio(ctx context.Context, thesis *model.Thesis, reason string) error {
	snapshot, err := json.Marshal(thesis)
	if err != nil {
		return err
	}
	instrument, err := json.Marshal(thesis.Instrument)
	if err != nil {
		return err
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO anti_portfolio (
			thesis_snapshot, rejection_reason, desk_id, strategy, instrument, direction,
			would_have_entry, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8
		)`,
		snapshot,
		reason,
		thesis.DeskID,
		thesis.Strategy,
		instrument,
		string(thesis.Direction),
		thesis.EntryPrice,
		time.Now(),
	)
	return err
}
