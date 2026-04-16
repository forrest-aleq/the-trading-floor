package wire

import (
	"math"
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
	if sig.EvidenceMeta.ConfidenceVector == nil || !sig.EvidenceMeta.ConfidenceVector.Present() {
		t.Fatalf("expected confidence vector to be computed, got %+v", sig.EvidenceMeta.ConfidenceVector)
	}
	if math.Abs(sig.EvidenceMeta.EvidenceScore-sig.EvidenceMeta.ConfidenceVector.Overall()) > 0.02 {
		t.Fatalf("expected evidence score to derive from confidence vector, got score=%.2f vector=%.2f",
			sig.EvidenceMeta.EvidenceScore, sig.EvidenceMeta.ConfidenceVector.Overall())
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

func TestCrossReferencerResolvesMultilingualEntityAliases(t *testing.T) {
	crossref := NewCrossReferencer(64, 8)

	first := NormalizeSignal(signal.Signal{
		ID:         "sig-nvda-en",
		Source:     "reuters",
		Type:       signal.TypeNews,
		Category:   "corporate",
		Timestamp:  time.Now(),
		Languages:  []string{"en"},
		Translated: "NVIDIA unveils a new data center roadmap",
		Entities: []signal.Entity{
			{Name: "NVIDIA", Type: "company"},
		},
	})
	second := NormalizeSignal(signal.Signal{
		ID:         "sig-nvda-zh",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "corporate",
		Timestamp:  time.Now(),
		Languages:  []string{"zh"},
		Translated: "英伟达发布新的数据中心路线图",
		Entities: []signal.Entity{
			{Name: "英伟达", Type: "company"},
		},
	})

	first = crossref.Enrich(first)
	second = crossref.Enrich(second)

	if len(second.RelatedSignalIDs) == 0 || second.RelatedSignalIDs[0] != "sig-nvda-en" {
		t.Fatalf("expected multilingual alias to resolve to the same entity, got %+v", second.RelatedSignalIDs)
	}
}

func TestNarrativeCorrelationCrossLinksLanguages(t *testing.T) {
	crossref := NewCrossReferencer(64, 8)
	clusterer := NewClusterer(128, 0.88)
	narratives := NewNarrativeCorrelator(128)

	first := NormalizeSignal(signal.Signal{
		ID:                    "sig-hormuz-ar",
		Source:                "telegram/mena",
		Type:                  signal.TypeNews,
		Category:              "geopolitical",
		Timestamp:             time.Now(),
		Languages:             []string{"ar"},
		OriginalText:          "اضطراب حركة الملاحة في مضيق هرمز",
		Translated:            "Shipping traffic disrupted in the Strait of Hormuz",
		TranslationProvider:   "source_payload",
		TranslationConfidence: 0.91,
		Entities: []signal.Entity{
			{Name: "Hormuz", Type: "region"},
		},
	})
	second := NormalizeSignal(signal.Signal{
		ID:                    "sig-hormuz-en",
		Source:                "ft-world",
		Type:                  signal.TypeNews,
		Category:              "geopolitical",
		Timestamp:             time.Now().Add(15 * time.Minute),
		Languages:             []string{"en"},
		OriginalText:          "Shipping traffic disrupted in the Strait of Hormuz",
		Translated:            "Shipping traffic disrupted in the Strait of Hormuz",
		TranslationProvider:   "identity",
		TranslationConfidence: 1,
		Entities: []signal.Entity{
			{Name: "Hormuz", Type: "region"},
		},
	})

	first = crossref.Enrich(narratives.Assign(clusterer.Assign(first)))
	second = crossref.Enrich(narratives.Assign(clusterer.Assign(second)))

	if second.NarrativeClusterID == "" || first.NarrativeClusterID != second.NarrativeClusterID {
		t.Fatalf("expected shared narrative cluster, got first=%q second=%q", first.NarrativeClusterID, second.NarrativeClusterID)
	}
	if len(second.CorroboratingLanguages) == 0 || second.CorroboratingLanguages[0] != "ar" {
		t.Fatalf("expected corroborating language from prior foreign-language source, got %+v", second.CorroboratingLanguages)
	}
	if second.EvidenceMeta == nil || second.EvidenceMeta.DistinctLanguages < 2 {
		t.Fatalf("expected multilingual corroboration metadata, got %+v", second.EvidenceMeta)
	}
}

func TestApplyLearnedSourceReliabilityBlendsTrustIntoEvidence(t *testing.T) {
	sig := NormalizeSignal(signal.Signal{
		ID:        "sig-learned-source",
		Source:    "stocktwits",
		Type:      signal.TypeSocial,
		Category:  "flows",
		Timestamp: time.Now(),
		Raw:       []byte(`{"title":"Flow desk is seeing unusual call buying"}`),
	})
	if sig.EvidenceMeta == nil {
		t.Fatal("expected normalized evidence metadata")
	}
	baselineTrust := sig.EvidenceMeta.SourceTrust
	baselineScore := sig.EvidenceMeta.EvidenceScore

	enriched := ApplyLearnedSourceReliability(sig, 0.88, 0.80)

	if enriched.EvidenceMeta == nil {
		t.Fatal("expected enriched evidence metadata")
	}
	if enriched.EvidenceMeta.SourceTrust <= baselineTrust {
		t.Fatalf("expected source trust to increase, got %.2f <= %.2f", enriched.EvidenceMeta.SourceTrust, baselineTrust)
	}
	if enriched.EvidenceMeta.EvidenceScore <= baselineScore {
		t.Fatalf("expected evidence score to increase, got %.2f <= %.2f", enriched.EvidenceMeta.EvidenceScore, baselineScore)
	}
	if sig.EvidenceMeta.SourceTrust != baselineTrust {
		t.Fatalf("expected original signal metadata to remain unchanged, got %.2f want %.2f", sig.EvidenceMeta.SourceTrust, baselineTrust)
	}
}
