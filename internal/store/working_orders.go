package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/pkg/model"
)

func (db *DB) UpsertWorkingOrder(ctx context.Context, record execution.PersistedOrder) error {
	orderID := record.Order.ID
	if orderID == "" {
		orderID = record.Snapshot.OrderID
	}
	if orderID == "" {
		return fmt.Errorf("working order missing order id")
	}
	record.Order.ID = orderID
	record.Snapshot.OrderID = orderID

	orderPayload, err := json.Marshal(record.Order)
	if err != nil {
		return err
	}
	snapshotPayload, err := json.Marshal(record.Snapshot)
	if err != nil {
		return err
	}

	var fillPayload []byte
	if record.Fill != nil {
		fillPayload, err = json.Marshal(record.Fill)
		if err != nil {
			return err
		}
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO working_orders (
			id, thesis_id, desk_id, state, broker_order_id, display_symbol, paper,
			submitted_at, updated_at, order_payload, snapshot_payload, fill_payload
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12
		)
		ON CONFLICT (id) DO UPDATE SET
			thesis_id = EXCLUDED.thesis_id,
			desk_id = EXCLUDED.desk_id,
			state = EXCLUDED.state,
			broker_order_id = EXCLUDED.broker_order_id,
			display_symbol = EXCLUDED.display_symbol,
			paper = EXCLUDED.paper,
			submitted_at = EXCLUDED.submitted_at,
			updated_at = EXCLUDED.updated_at,
			order_payload = EXCLUDED.order_payload,
			snapshot_payload = EXCLUDED.snapshot_payload,
			fill_payload = EXCLUDED.fill_payload`,
		orderID,
		record.Snapshot.ThesisID,
		record.Snapshot.DeskID,
		string(record.Snapshot.State),
		record.Snapshot.BrokerOrderID,
		record.Snapshot.DisplaySymbol,
		record.Snapshot.Paper,
		record.Snapshot.SubmittedAt,
		record.Snapshot.UpdatedAt,
		orderPayload,
		snapshotPayload,
		fillPayload,
	)
	return err
}

func (db *DB) LoadWorkingOrders(ctx context.Context) ([]execution.PersistedOrder, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT order_payload, snapshot_payload, fill_payload
		   FROM working_orders
		  WHERE state = $1 OR state = $2
		  ORDER BY submitted_at ASC NULLS LAST, updated_at ASC`,
		string(execution.OrderStateWorking),
		string(execution.OrderStatePartiallyFilled),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]execution.PersistedOrder, 0)
	for rows.Next() {
		var (
			orderPayload    []byte
			snapshotPayload []byte
			fillPayload     []byte
		)
		if err := rows.Scan(&orderPayload, &snapshotPayload, &fillPayload); err != nil {
			return nil, err
		}

		record := execution.PersistedOrder{}
		if err := json.Unmarshal(orderPayload, &record.Order); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(snapshotPayload, &record.Snapshot); err != nil {
			return nil, err
		}
		if len(fillPayload) > 0 {
			fill := &model.Fill{}
			if err := json.Unmarshal(fillPayload, fill); err != nil {
				return nil, err
			}
			record.Fill = fill
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
