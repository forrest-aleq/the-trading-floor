package store

import (
	"context"
	"encoding/json"

	"github.com/hnic/trading-floor/pkg/signal"
)

func (db *DB) UpsertSignal(ctx context.Context, sig signal.Signal) error {
	entities, err := json.Marshal(sig.Entities)
	if err != nil {
		return err
	}

	language := ""
	if len(sig.Languages) > 0 {
		language = sig.Languages[0]
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO signals (
			id, source, type, category, content, language, translated, entities,
			urgency, strength, direction, content_hash, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13
		)
		ON CONFLICT (id) DO UPDATE SET
			translated = EXCLUDED.translated,
			entities = EXCLUDED.entities,
			urgency = EXCLUDED.urgency,
			strength = EXCLUDED.strength,
			direction = EXCLUDED.direction,
			content_hash = EXCLUDED.content_hash`,
		sig.ID,
		sig.Source,
		string(sig.Type),
		sig.Category,
		string(sig.Raw),
		language,
		sig.Translated,
		entities,
		sig.Urgency,
		sig.Strength,
		string(sig.Direction),
		sig.ContentHash,
		sig.Timestamp,
	)
	return err
}
