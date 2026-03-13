package trace

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ctxKey int

const traceKey ctxKey = 0

// Span carries trace context through the full decision chain.
type Span struct {
	TraceID   string `json:"trace_id"`
	SessionID string `json:"session_id"`
	DeskID    string `json:"desk_id"`
	Stage     string `json:"stage"`     // wire, scanner, research, prosecutor, risk, execution, book
	SignalID  string `json:"signal_id"` // originating signal
	StartedAt time.Time `json:"started_at"`
}

// New creates a root span for a signal entering the pipeline.
func New(sessionID, deskID, signalID string) Span {
	return Span{
		TraceID:   uuid.New().String(),
		SessionID: sessionID,
		DeskID:    deskID,
		SignalID:  signalID,
		Stage:     "wire",
		StartedAt: time.Now(),
	}
}

// WithStage returns a copy of the span advanced to the given pipeline stage.
func (s Span) WithStage(stage string) Span {
	s.Stage = stage
	return s
}

// Fields returns slog-compatible key-value pairs for structured logging.
func (s Span) Fields() []any {
	return []any{
		"trace_id", s.TraceID,
		"session_id", s.SessionID,
		"desk_id", s.DeskID,
		"stage", s.Stage,
		"signal_id", s.SignalID,
	}
}

// IntoContext stores the span in context.
func IntoContext(ctx context.Context, span Span) context.Context {
	return context.WithValue(ctx, traceKey, span)
}

// FromContext retrieves the span from context. Returns a zero span if absent.
func FromContext(ctx context.Context) Span {
	if s, ok := ctx.Value(traceKey).(Span); ok {
		return s
	}
	return Span{}
}

// ForLLM returns the trace header string to include in LLM request metadata.
func (s Span) ForLLM() string {
	return fmt.Sprintf("trace=%s desk=%s stage=%s signal=%s", s.TraceID, s.DeskID, s.Stage, s.SignalID)
}
