package wire

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestDeduperCatchesNearDuplicates(t *testing.T) {
	deduper := NewDeduper(128, 0.92)

	first := NormalizeSignal(signal.Signal{
		ID:        "1",
		Source:    "reuters",
		Type:      signal.TypeNews,
		Category:  "macro",
		Timestamp: time.Now(),
		Raw:       []byte(`{"title":"Fed signals higher rates for longer as inflation risk persists","description":"Officials say policy may stay tight while inflation risks remain elevated"}`),
	})
	second := NormalizeSignal(signal.Signal{
		ID:        "2",
		Source:    "wsj",
		Type:      signal.TypeNews,
		Category:  "macro",
		Timestamp: time.Now(),
		Raw:       []byte(`{"title":"Federal Reserve says rates could stay higher for longer as inflation risk remains","description":"Policymakers indicate policy may remain tight because inflation risks are still elevated"}`),
	})

	if deduper.IsDuplicate(first) {
		t.Fatal("first signal should not be duplicate")
	}
	if !deduper.IsDuplicate(second) {
		t.Fatal("expected semantic near-duplicate to be detected")
	}
}
