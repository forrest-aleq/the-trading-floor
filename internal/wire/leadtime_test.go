package wire

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestLeadTimeTrackerLearnsFromEarlierForeignLanguageSignals(t *testing.T) {
	tracker := NewLeadTimeTracker(128, 8)

	early := NormalizeSignal(signal.Signal{
		ID:                    "sig-early-ar",
		Source:                "telegram/mena",
		Type:                  signal.TypeNews,
		Category:              "geopolitical",
		Timestamp:             time.Date(2026, 3, 20, 9, 0, 0, 0, time.UTC),
		Languages:             []string{"ar"},
		OriginalText:          "اضطراب الملاحة في مضيق هرمز",
		Translated:            "Shipping traffic disrupted in the Strait of Hormuz",
		TranslationProvider:   "source_payload",
		TranslationConfidence: 0.91,
		NarrativeClusterID:    "narrative-hormuz",
	})
	early = tracker.Enrich(early)

	consensus := NormalizeSignal(signal.Signal{
		ID:                    "sig-consensus-en",
		Source:                "ft-world",
		Type:                  signal.TypeNews,
		Category:              "geopolitical",
		Timestamp:             time.Date(2026, 3, 20, 11, 0, 0, 0, time.UTC),
		Languages:             []string{"en"},
		OriginalText:          "Shipping traffic disrupted in the Strait of Hormuz",
		Translated:            "Shipping traffic disrupted in the Strait of Hormuz",
		TranslationProvider:   "identity",
		TranslationConfidence: 1,
		NarrativeClusterID:    "narrative-hormuz",
	})
	_ = tracker.Enrich(consensus)

	followOn := NormalizeSignal(signal.Signal{
		ID:                    "sig-follow-ar",
		Source:                "telegram/mena",
		Type:                  signal.TypeNews,
		Category:              "geopolitical",
		Timestamp:             time.Date(2026, 3, 21, 9, 0, 0, 0, time.UTC),
		Languages:             []string{"ar"},
		OriginalText:          "تقارير جديدة عن اضطراب الملاحة",
		Translated:            "Fresh reports of shipping disruption",
		TranslationProvider:   "source_payload",
		TranslationConfidence: 0.89,
		NarrativeClusterID:    "narrative-hormuz-2",
	})
	followOn = tracker.Enrich(followOn)

	if followOn.EvidenceMeta == nil {
		t.Fatal("expected evidence metadata after lead-time enrichment")
	}
	if followOn.EvidenceMeta.OriginRegion != "mena" {
		t.Fatalf("expected MENA region inference, got %q", followOn.EvidenceMeta.OriginRegion)
	}
	if followOn.EvidenceMeta.LeadTimeObservations == 0 {
		t.Fatalf("expected learned lead-time observations, got %+v", followOn.EvidenceMeta)
	}
	if followOn.EvidenceMeta.LeadTimeAverageHours < 1.9 || followOn.EvidenceMeta.LeadTimeAverageHours > 2.1 {
		t.Fatalf("expected ~2h lead time, got %.2f", followOn.EvidenceMeta.LeadTimeAverageHours)
	}
	if followOn.EvidenceMeta.LeadTimeScore <= 0 {
		t.Fatalf("expected positive lead-time score, got %.2f", followOn.EvidenceMeta.LeadTimeScore)
	}
}
