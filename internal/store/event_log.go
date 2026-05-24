package store

import (
	"context"
	"encoding/json"
	"time"
)

type EventLogEntry struct {
	Timestamp time.Time
	EventType string
	SessionID string
	TraceID   string
	DeskID    string
	Severity  string
	Message   string
	Metadata  map[string]any
}

func (db *DB) InsertEventLog(ctx context.Context, entry EventLogEntry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	if entry.Severity == "" {
		entry.Severity = "info"
	}
	metadata, err := json.Marshal(entry.Metadata)
	if err != nil {
		return err
	}
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO event_log (
			timestamp, event_type, session_id, trace_id, desk_id, severity, message, metadata
		) VALUES (
			$1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8
		)`,
		entry.Timestamp,
		entry.EventType,
		entry.SessionID,
		entry.TraceID,
		entry.DeskID,
		entry.Severity,
		entry.Message,
		metadata,
	)
	return err
}
