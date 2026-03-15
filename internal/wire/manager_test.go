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
