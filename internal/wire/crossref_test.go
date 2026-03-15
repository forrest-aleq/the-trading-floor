package wire

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestCrossReferencerAddsCorroborationAcrossSourcesAndEntities(t *testing.T) {
	clusterer := NewClusterer(128, 0.88)
	crossref := NewCrossReferencer(64, 8)

	first := NormalizeSignal(signal.Signal{
		ID:        "sig-1",
		Source:    "reuters",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Entities: []signal.Entity{
			{Name: "AAPL", Type: "instrument"},
		},
		Raw: []byte(`{"title":"Apple shifts more supplier capacity into India"}`),
	})
	second := NormalizeSignal(signal.Signal{
		ID:        "sig-2",
		Source:    "ft",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Entities: []signal.Entity{
			{Name: "AAPL", Type: "instrument"},
		},
		Raw: []byte(`{"title":"Apple expands India supplier footprint as assembly diversifies"}`),
	})

	first = crossref.Enrich(clusterer.Assign(first))
	second = crossref.Enrich(clusterer.Assign(second))

	if len(second.RelatedSignalIDs) == 0 || second.RelatedSignalIDs[0] != "sig-1" {
		t.Fatalf("expected second signal to reference prior signal, got %+v", second.RelatedSignalIDs)
	}
	if len(second.CorroboratingSources) != 1 || second.CorroboratingSources[0] != "reuters" {
		t.Fatalf("expected corroborating source, got %+v", second.CorroboratingSources)
	}
	if len(second.CorroboratingEntities) != 1 || second.CorroboratingEntities[0] != "AAPL" {
		t.Fatalf("expected corroborating entity, got %+v", second.CorroboratingEntities)
	}
}
