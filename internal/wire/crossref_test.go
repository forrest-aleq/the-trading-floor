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

func TestNormalizeSignalInfersEvidenceMetadata(t *testing.T) {
	sig := NormalizeSignal(signal.Signal{
		ID:        "sig-edgar",
		Source:    "sec-edgar",
		Type:      signal.TypeFiling,
		Category:  "corporate",
		Timestamp: time.Now(),
		Raw:       []byte(`{"form_type":"8-K","link":"https://www.sec.gov/ixviewer/ix.html","company":"Acme Corp"}`),
	})

	if sig.EvidenceMeta == nil {
		t.Fatal("expected evidence metadata to be attached during normalization")
	}
	if sig.EvidenceMeta.SourceTier != "primary" {
		t.Fatalf("expected primary source tier, got %q", sig.EvidenceMeta.SourceTier)
	}
	if sig.EvidenceMeta.SourceOwnerGroup != "sec" {
		t.Fatalf("expected sec owner group, got %q", sig.EvidenceMeta.SourceOwnerGroup)
	}
	if sig.EvidenceMeta.FreshnessWindowHours != 48 {
		t.Fatalf("expected 8-K freshness window of 48h, got %.1f", sig.EvidenceMeta.FreshnessWindowHours)
	}
}

func TestCrossReferencerDetectsContradictorySignalsAcrossIndependentOwners(t *testing.T) {
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
		Translated: "Apple revenue increased to $10B and management raised guidance.",
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
		Translated: "Apple revenue declined to $7B and management cut guidance.",
	})

	first = crossref.Enrich(first)
	second = crossref.Enrich(second)

	if second.EvidenceMeta == nil {
		t.Fatal("expected evidence metadata on enriched signal")
	}
	if second.EvidenceMeta.DistinctOwnerGroups < 2 {
		t.Fatalf("expected independent owner groups, got %d", second.EvidenceMeta.DistinctOwnerGroups)
	}
	if second.EvidenceMeta.ContradictionCount == 0 {
		t.Fatalf("expected contradiction to be detected, got %+v", second.EvidenceMeta)
	}
	if second.EvidenceMeta.ContradictionSeverity == "" {
		t.Fatalf("expected contradiction severity, got %+v", second.EvidenceMeta)
	}
	if len(second.EvidenceMeta.ConflictingSignalIDs) == 0 || second.EvidenceMeta.ConflictingSignalIDs[0] != "sig-1" {
		t.Fatalf("expected conflicting signal id, got %+v", second.EvidenceMeta.ConflictingSignalIDs)
	}
}
