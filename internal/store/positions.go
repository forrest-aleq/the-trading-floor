package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func (db *DB) UpsertPosition(ctx context.Context, pos *model.Position) error {
	instrument, err := json.Marshal(pos.Instrument)
	if err != nil {
		return err
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO positions (
			id, thesis_id, desk_id, instrument, direction, quantity, entry_price, current_price,
			unrealized_pnl, realized_pnl, ibkr_order_id, ibkr_contract_id, status, opened_at, closed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15
		)
		ON CONFLICT (id) DO UPDATE SET
			current_price = EXCLUDED.current_price,
			unrealized_pnl = EXCLUDED.unrealized_pnl,
			realized_pnl = EXCLUDED.realized_pnl,
			status = EXCLUDED.status,
			closed_at = EXCLUDED.closed_at`,
		pos.ID,
		pos.ThesisID,
		pos.DeskID,
		instrument,
		string(pos.Direction),
		pos.Quantity,
		pos.EntryPrice,
		pos.CurrentPrice,
		pos.UnrealizedPnL,
		pos.RealizedPnL,
		pos.IBKROrderID,
		pos.IBKRContractID,
		pos.Status,
		pos.OpenedAt,
		pos.ClosedAt,
	)
	return err
}

func (db *DB) UpdatePositionClose(ctx context.Context, id string, pnl float64, exitPrice float64, closedAt time.Time) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE positions
		    SET status = 'closed',
		        realized_pnl = $2,
		        current_price = $3,
		        closed_at = $4
		  WHERE id = $1`,
		id,
		pnl,
		exitPrice,
		closedAt,
	)
	return err
}
