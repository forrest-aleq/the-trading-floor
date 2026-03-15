package feeds

import (
	"testing"
	"time"
)

func TestSourceStateBackoffAndSeenLimit(t *testing.T) {
	state := newSourceState(2)
	now := time.Now()

	if state.Seen("a") {
		t.Fatal("first id should be new")
	}
	if state.Seen("b") {
		t.Fatal("second id should be new")
	}
	if !state.Seen("a") {
		t.Fatal("expected duplicate id to be recognized")
	}
	if state.Seen("c") {
		t.Fatal("third distinct id should be new")
	}
	if state.Seen("a") {
		t.Fatal("oldest id should have been pruned")
	}

	backoff := state.RecordFailure(now, 30*time.Second)
	if backoff != 30*time.Second {
		t.Fatalf("unexpected first backoff: %s", backoff)
	}
	backoff = state.RecordFailure(now, 30*time.Second)
	if backoff != time.Minute {
		t.Fatalf("unexpected second backoff: %s", backoff)
	}
	if skip, _ := state.ShouldPoll(now.Add(45 * time.Second)); !skip {
		t.Fatal("expected source to remain in backoff window")
	}

	state.RecordSuccess()
	if skip, _ := state.ShouldPoll(now.Add(45 * time.Second)); skip {
		t.Fatal("expected success to clear backoff")
	}
}

func TestParsePublishedTime(t *testing.T) {
	for _, raw := range []string{
		"2026-03-14T20:39:17Z",
		"Sat, 14 Mar 2026 20:39:17 GMT",
		"2026-03-14",
	} {
		if _, ok := parsePublishedTime(raw); !ok {
			t.Fatalf("expected to parse %q", raw)
		}
	}
}
