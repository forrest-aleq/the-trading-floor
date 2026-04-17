package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

type positionStore interface {
	UpsertPosition(context.Context, *model.Position) error
}

type positionPersistenceWriter struct {
	log           *slog.Logger
	store         positionStore
	flushInterval time.Duration
	writeTimeout  time.Duration

	mu      sync.Mutex
	pending map[string]*model.Position
}

func newPositionPersistenceWriter(store positionStore, flushInterval, writeTimeout time.Duration) *positionPersistenceWriter {
	if flushInterval <= 0 {
		flushInterval = 2 * time.Second
	}
	if writeTimeout <= 0 {
		writeTimeout = 15 * time.Second
	}
	return &positionPersistenceWriter{
		log:           slog.Default().With("component", "position_persist"),
		store:         store,
		flushInterval: flushInterval,
		writeTimeout:  writeTimeout,
		pending:       make(map[string]*model.Position),
	}
}

func (w *positionPersistenceWriter) Enqueue(positions []*model.Position) int {
	if w == nil || w.store == nil || len(positions) == 0 {
		return 0
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	count := 0
	for _, pos := range positions {
		if pos == nil || pos.ID == "" {
			continue
		}
		w.pending[pos.ID] = clonePosition(pos)
		count++
	}
	return count
}

func (w *positionPersistenceWriter) Run(ctx context.Context) {
	if w == nil || w.store == nil {
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.flush(context.Background())
			return
		case <-ticker.C:
			w.flush(ctx)
		}
	}
}

func (w *positionPersistenceWriter) flush(parent context.Context) {
	if w == nil || w.store == nil {
		return
	}

	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}
	batch := make([]*model.Position, 0, len(w.pending))
	for _, pos := range w.pending {
		batch = append(batch, pos)
	}
	w.pending = make(map[string]*model.Position)
	w.mu.Unlock()

	ctx, cancel := context.WithTimeout(parent, w.writeTimeout)
	defer cancel()

	for _, pos := range batch {
		if err := w.store.UpsertPosition(ctx, pos); err != nil {
			w.log.Warn("async position persistence failed", "position_id", pos.ID, "error", err)
			w.mu.Lock()
			w.pending[pos.ID] = pos
			w.mu.Unlock()
		}
	}
}

func clonePosition(pos *model.Position) *model.Position {
	if pos == nil {
		return nil
	}
	cloned := *pos
	if len(pos.Legs) > 0 {
		cloned.Legs = append([]model.TradeLeg(nil), pos.Legs...)
	}
	if pos.ClosedAt != nil {
		closedAt := *pos.ClosedAt
		cloned.ClosedAt = &closedAt
	}
	return &cloned
}
