package wire

import (
	"context"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

type testFeed struct {
	name    string
	signals []signal.Signal
}

func (f testFeed) Name() string {
	return f.name
}

func (f testFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	for _, sig := range f.signals {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- sig:
		}
	}
	return nil
}

func TestManagerReplaysOverflowedSignals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	manager := NewManager()
	manager.bufferSize = 1
	manager.maxOverflow = 8
	manager.retryInterval = 5 * time.Millisecond

	manager.RegisterFeed(testFeed{
		name: "burst",
		signals: []signal.Signal{
			{
				ID:        "sig-1",
				Source:    "feed-a",
				Type:      signal.TypeNews,
				Category:  "corporate",
				Timestamp: time.Now(),
				Raw:       []byte(`{"title":"Nvidia wins hyperscaler supply agreement"}`),
			},
			{
				ID:        "sig-2",
				Source:    "feed-a",
				Type:      signal.TypeNews,
				Category:  "corporate",
				Timestamp: time.Now(),
				Raw:       []byte(`{"title":"AMD lands a separate hyperscaler accelerator contract"}`),
			},
		},
	})

	sub := manager.Subscribe()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}

	waitFor(t, 500*time.Millisecond, func() bool {
		return manager.Stats().TotalOverflowed == 1
	})

	first := receiveSignal(t, sub, time.Second)
	if first.ID != "sig-1" {
		t.Fatalf("expected first signal to be delivered immediately, got %s", first.ID)
	}

	second := receiveSignal(t, sub, time.Second)
	if second.ID != "sig-2" {
		t.Fatalf("expected overflowed signal to be replayed, got %s", second.ID)
	}

	waitFor(t, 500*time.Millisecond, func() bool {
		stats := manager.Stats()
		return stats.TotalReplayed == 1 && stats.PendingOverflow == 0
	})

	stats := manager.Stats()
	if stats.TotalDropped != 0 {
		t.Fatalf("expected no dropped signals, got %d", stats.TotalDropped)
	}
}

func TestManagerRecordsDeadLettersWhenOverflowIsExhausted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	manager := NewManager()
	manager.bufferSize = 1
	manager.maxOverflow = 1
	manager.maxDeadLetters = 8
	manager.retryInterval = 5 * time.Millisecond

	manager.RegisterFeed(testFeed{
		name: "burst",
		signals: []signal.Signal{
			{ID: "sig-1", Source: "feed-a", Type: signal.TypeNews, Category: "macro-fed", Timestamp: time.Now(), Raw: []byte(`{"title":"fed speaker one"}`)},
			{ID: "sig-2", Source: "feed-a", Type: signal.TypeNews, Category: "macro-rates", Timestamp: time.Now(), Raw: []byte(`{"title":"cpi surprise two"}`)},
			{ID: "sig-3", Source: "feed-a", Type: signal.TypeNews, Category: "macro-fx", Timestamp: time.Now(), Raw: []byte(`{"title":"yen intervention three"}`)},
		},
	})

	_ = manager.Subscribe()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stats := manager.Stats()
		if stats.TotalDropped == 1 && stats.TotalDeadLettered == 1 && stats.PendingDeadLetters == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stats := manager.Stats()
	if stats.TotalDropped != 1 || stats.TotalDeadLettered != 1 || stats.PendingDeadLetters != 1 {
		t.Fatalf("unexpected wire stats after overflow exhaustion: %+v dead_letters=%+v", stats, manager.DeadLetters(10))
	}

	letters := manager.DeadLetters(10)
	if len(letters) != 1 {
		t.Fatalf("expected one dead letter, got %d", len(letters))
	}
	if letters[0].SignalID != "sig-3" {
		t.Fatalf("expected sig-3 to dead-letter, got %s", letters[0].SignalID)
	}
	if letters[0].Reason != "subscriber_overflow_exhausted" {
		t.Fatalf("unexpected dead letter reason %q", letters[0].Reason)
	}
	if letters[0].DroppedAt.IsZero() {
		t.Fatal("expected dead letter timestamp to be recorded")
	}
}

func receiveSignal(t *testing.T, ch <-chan signal.Signal, timeout time.Duration) signal.Signal {
	t.Helper()

	select {
	case sig := <-ch:
		return sig
	case <-time.After(timeout):
		t.Fatal("timed out waiting for signal")
		return signal.Signal{}
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}
