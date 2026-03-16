package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hnic/trading-floor/pkg/signal"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrDuplicateSignalContentHash = errors.New("duplicate signal content hash")

func (db *DB) UpsertSignal(ctx context.Context, sig signal.Signal) error {
	entities, err := json.Marshal(sig.Entities)
	if err != nil {
		return err
	}

	language := ""
	if len(sig.Languages) > 0 {
		language = sig.Languages[0]
	}

	embeddingExpr := "$12"
	var embeddingValue any
	if db.signalEmbeddingIsVector {
		embeddingExpr = "NULLIF($12, '')::vector"
		embeddingValue = vectorLiteral(sig.Embedding)
	} else {
		embeddingValue = embeddingArray(sig.Embedding)
	}

	_, err = db.Pool.Exec(ctx,
		fmt.Sprintf(`INSERT INTO signals (
			id, source, type, category, content, language, translated, entities,
			urgency, strength, direction, embedding, content_hash, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, %s, $13, $14
		)
		ON CONFLICT (id) DO UPDATE SET
			translated = EXCLUDED.translated,
			entities = EXCLUDED.entities,
			urgency = EXCLUDED.urgency,
			strength = EXCLUDED.strength,
			direction = EXCLUDED.direction,
			embedding = EXCLUDED.embedding,
			content_hash = EXCLUDED.content_hash`, embeddingExpr),
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
		embeddingValue,
		sig.ContentHash,
		sig.Timestamp,
	)
	return classifySignalWriteError(err)
}

func classifySignalWriteError(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_signals_content_hash" {
		return ErrDuplicateSignalContentHash
	}

	return err
}

func vectorLiteral(embedding []float32) string {
	if len(embedding) == 0 {
		return ""
	}

	parts := make([]string, len(embedding))
	for i, value := range embedding {
		parts[i] = fmt.Sprintf("%.6f", value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func embeddingArray(embedding []float32) []float64 {
	if len(embedding) == 0 {
		return nil
	}

	values := make([]float64, len(embedding))
	for i, value := range embedding {
		values[i] = float64(value)
	}
	return values
}
