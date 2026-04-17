package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

type stubPositionStore struct {
	mu        sync.Mutex
	positions []*model.Position
}

func (s *stubPositionStore) UpsertPosition(_ context.Context, pos *model.Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.positions = append(s.positions, clonePosition(pos))
	return nil
}

func TestPositionPersistenceWriterCoalescesByPositionID(t *testing.T) {
	store := &stubPositionStore{}
	writer := newPositionPersistenceWriter(store, time.Second, time.Second)

	writer.Enqueue([]*model.Position{{
		ID:           "pos-1",
		DeskID:       "desk-a",
		CurrentPrice: 101,
	}})
	writer.Enqueue([]*model.Position{{
		ID:           "pos-1",
		DeskID:       "desk-a",
		CurrentPrice: 103.5,
	}})

	writer.flush(context.Background())

	if len(store.positions) != 1 {
		t.Fatalf("expected one coalesced position write, got %d", len(store.positions))
	}
	if store.positions[0].CurrentPrice != 103.5 {
		t.Fatalf("expected latest current price 103.5, got %.2f", store.positions[0].CurrentPrice)
	}
}

func TestPositionPersistenceWriterClonesEnqueuedPositions(t *testing.T) {
	store := &stubPositionStore{}
	writer := newPositionPersistenceWriter(store, time.Second, time.Second)

	pos := &model.Position{
		ID:           "pos-2",
		DeskID:       "desk-b",
		CurrentPrice: 99.25,
	}
	writer.Enqueue([]*model.Position{pos})
	pos.CurrentPrice = 150

	writer.flush(context.Background())

	if len(store.positions) != 1 {
		t.Fatalf("expected one stored position, got %d", len(store.positions))
	}
	if store.positions[0].CurrentPrice != 99.25 {
		t.Fatalf("expected cloned current price 99.25, got %.2f", store.positions[0].CurrentPrice)
	}
}
