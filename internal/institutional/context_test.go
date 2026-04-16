package institutional

import (
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

func TestBuildCollaborationContext(t *testing.T) {
	context := BuildCollaborationContext(&model.CollaborationInput{
		OriginDesk:             "desk-geo-a",
		OriginDomain:           "macro",
		Kind:                   model.ColleagueMessageProposal,
		RequestedAction:        "review",
		Summary:                "Internal thesis from geo desk",
		RelationshipTrust:      0.77,
		RelationshipConfidence: 0.61,
	}, "  ")

	for _, want := range []string{
		"Institutional context:",
		"colleague.from_desk=desk-geo-a",
		"colleague.from_domain=macro",
		"colleague.peer_trust=0.77",
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("context missing %q\n%s", want, context)
		}
	}
}

func TestBuildSignalContextIncludesInstitutionalAndEvidenceState(t *testing.T) {
	sig := signal.Signal{
		ID:                   "sig-1",
		Source:               "reuters",
		Type:                 signal.TypeNews,
		Category:             "macro",
		Timestamp:            time.Now(),
		Urgency:              0.84,
		Languages:            []string{"ar"},
		InstitutionalContext: "Institutional context:\n  colleague.from_desk=desk-geo-a",
		Translated:           "Federal Reserve speech signaled a more hawkish balance of risks.",
		Entities:             []signal.Entity{{Name: "TLT", Type: "instrument"}},
		EvidenceMeta: &evidence.Metadata{
			SourceTrust:          0.95,
			SourceTier:           "primary",
			SourceType:           "primary",
			LeadTimeAverageHours: 2.4,
			LeadTimeObservations: 3,
			LeadTimeScore:        0.41,
			EvidenceScore:        0.88,
			ConfidenceVector: &evidence.ConfidenceVector{
				FactConfidence:          0.92,
				NoveltyConfidence:       0.70,
				MarketMappingConfidence: 0.81,
				ExpressionConfidence:    0.77,
				ExecutionConfidence:     0.79,
				CompetenceConfidence:    0.74,
			},
		},
	}

	formatted := BuildSignalContext(sig, SignalContextOptions{
		ContentLimit:         220,
		RelatedLimit:         4,
		EntityLimit:          8,
		IncludeEvidence:      true,
		IncludeInstitutional: true,
	})

	for _, want := range []string{
		"Institutional context:",
		"colleague.from_desk=desk-geo-a",
		"Source: reuters",
		"Category: macro",
		"Urgency: 0.84",
		"Source trust: 0.95",
		"Historical lead time: avg 2.40h across 3 narratives (score 0.41)",
		"Content: Federal Reserve speech signaled a more hawkish balance of risks.",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted context missing %q\n%s", want, formatted)
		}
	}
}
