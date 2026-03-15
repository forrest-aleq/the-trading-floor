package scanner

import (
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestFormatSignalIncludesCrossReferenceContext(t *testing.T) {
	formatted := formatSignal(signal.Signal{
		ID:                    "sig-1",
		Source:                "ft",
		Type:                  signal.TypeNews,
		Category:              "corporate",
		Timestamp:             time.Now(),
		Urgency:               0.8,
		ClusterID:             "cluster-123",
		RelatedSignalIDs:      []string{"sig-a", "sig-b"},
		CorroboratingSources:  []string{"reuters", "fed-press"},
		CorroboratingEntities: []string{"AAPL"},
		Translated:            "Apple expands India supplier footprint",
		Entities: []signal.Entity{
			{Name: "AAPL", Type: "instrument"},
		},
	})

	for _, want := range []string{
		"Cluster: cluster-123",
		"Related signals: 2",
		"Corroborating sources: reuters, fed-press",
		"Corroborating entities: AAPL",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted signal missing %q\n%s", want, formatted)
		}
	}
}

func TestFormatSignalTruncatesLongContent(t *testing.T) {
	formatted := formatSignal(signal.Signal{
		Source:     "fed-press",
		Type:       signal.TypeNews,
		Category:   "macro",
		Translated: strings.Repeat("a", 1500),
	})

	if strings.Contains(formatted, strings.Repeat("a", 1300)) {
		t.Fatalf("expected long content to be truncated\n%s", formatted)
	}
	if !strings.Contains(formatted, "...") {
		t.Fatalf("expected truncated content to include ellipsis\n%s", formatted)
	}
}
