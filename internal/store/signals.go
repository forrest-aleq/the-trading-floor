package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

type SignalQuery struct {
	Limit int
	Since time.Time
}

func (db *DB) ListSignals(ctx context.Context, query SignalQuery) ([]signal.Signal, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}

	sql := `
		SELECT id, source, type, category, content, language, translated, entities,
		       urgency, strength, direction, content_hash, created_at
		FROM signals`

	args := make([]any, 0, 2)
	if !query.Since.IsZero() {
		sql += ` WHERE created_at >= $1`
		args = append(args, query.Since)
	}
	sql += ` ORDER BY created_at ASC`
	if len(args) == 0 {
		sql += ` LIMIT $1`
		args = append(args, limit)
	} else {
		sql += ` LIMIT $2`
		args = append(args, limit)
	}

	rows, err := db.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query signals: %w", err)
	}
	defer rows.Close()

	signals := make([]signal.Signal, 0, limit)
	for rows.Next() {
		var (
			sig        signal.Signal
			content    string
			language   string
			entities   []byte
			direction  string
			typeValue  string
			category   string
			translated string
		)
		if err := rows.Scan(
			&sig.ID,
			&sig.Source,
			&typeValue,
			&category,
			&content,
			&language,
			&translated,
			&entities,
			&sig.Urgency,
			&sig.Strength,
			&direction,
			&sig.ContentHash,
			&sig.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("scan signal row: %w", err)
		}

		sig.Type = signal.Type(typeValue)
		sig.Category = category
		sig.Direction = signal.Direction(direction)
		sig.Translated = translated
		if language != "" {
			sig.Languages = []string{language}
		}
		if content != "" {
			sig.Raw = json.RawMessage(content)
		}
		if len(entities) > 0 {
			if err := json.Unmarshal(entities, &sig.Entities); err != nil {
				return nil, fmt.Errorf("decode signal entities: %w", err)
			}
		}

		signals = append(signals, sig)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate signals: %w", err)
	}
	return signals, nil
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
