package observe

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditLog records every decision, every trade, every belief update.
// Append-only. Immutable. The full history of the system.
type AuditLog struct {
	mu   sync.Mutex
	log  *slog.Logger
	file *os.File
	enc  *json.Encoder
}

type AuditEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Type      string      `json:"type"`
	DeskID    string      `json:"desk_id,omitempty"`
	ThesisID  string      `json:"thesis_id,omitempty"`
	Data      interface{} `json:"data"`
}

func NewAuditLog(path string) (*AuditLog, error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &AuditLog{
		log:  slog.Default().With("component", "audit"),
		file: f,
		enc:  json.NewEncoder(f),
	}, nil
}

func (a *AuditLog) Record(entryType string, deskID string, thesisID string, data interface{}) {
	entry := AuditEntry{
		Timestamp: time.Now(),
		Type:      entryType,
		DeskID:    deskID,
		ThesisID:  thesisID,
		Data:      data,
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.enc.Encode(entry); err != nil {
		a.log.Error("audit write failed", "error", err)
	}
}

func (a *AuditLog) Close() error {
	return a.file.Close()
}
