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
	legs, err := json.Marshal(pos.Legs)
	if err != nil {
		return err
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO positions (
			id, thesis_id, desk_id, structure, instrument, legs, direction, quantity, entry_price, current_price,
			unrealized_pnl, realized_pnl, ibkr_order_id, ibkr_contract_id, shadow, status, opened_at, closed_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
				$11, $12, $13, $14, $15, $16, $17, $18
			)
			ON CONFLICT (id) DO UPDATE SET
				thesis_id = EXCLUDED.thesis_id,
				desk_id = EXCLUDED.desk_id,
				structure = EXCLUDED.structure,
				instrument = EXCLUDED.instrument,
				legs = EXCLUDED.legs,
				direction = EXCLUDED.direction,
				quantity = EXCLUDED.quantity,
				entry_price = EXCLUDED.entry_price,
				current_price = EXCLUDED.current_price,
				unrealized_pnl = EXCLUDED.unrealized_pnl,
				realized_pnl = EXCLUDED.realized_pnl,
				ibkr_order_id = EXCLUDED.ibkr_order_id,
				ibkr_contract_id = EXCLUDED.ibkr_contract_id,
				shadow = EXCLUDED.shadow,
				status = EXCLUDED.status,
				opened_at = EXCLUDED.opened_at,
				closed_at = EXCLUDED.closed_at`,
		pos.ID,
		pos.ThesisID,
		pos.DeskID,
		pos.Structure,
		instrument,
		legs,
		string(pos.Direction),
		pos.Quantity,
		pos.EntryPrice,
		pos.CurrentPrice,
		pos.UnrealizedPnL,
		pos.RealizedPnL,
		pos.IBKROrderID,
		pos.IBKRContractID,
		pos.Shadow,
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
