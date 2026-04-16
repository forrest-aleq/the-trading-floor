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
		RelationshipHealth:     0.72,
		RecoveryScore:          0.18,
	}, "  ")

	for _, want := range []string{
		"Institutional context:",
		"colleague.from_desk=desk-geo-a",
		"colleague.from_domain=macro",
		"colleague.peer_trust=0.77",
		"colleague.relationship_health=0.72",
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
		Expectation: &model.ExpectationState{
			Domain:               "macro",
			PredictedImportance:  0.84,
			PredictedReliability: 0.82,
			PredictedTradability: 0.77,
			PredictedNovelty:     0.70,
			PredictedDirection:   "bullish",
			PredictedAction:      "investigate",
		},
		Appraisal: &model.AppraisalState{
			Domain:             "macro",
			ViolationClass:     "positive_surprise",
			ViolationScore:     0.21,
			ExpectationGap:     0.19,
			ActionPressure:     0.88,
			Power:              0.77,
			Distance:           0.34,
			Rank:               0.84,
			FaceThreatScore:    0.04,
			SocialCost:         0.08,
			RelationshipHealth: 0.83,
		},
		ActionSelection: &model.ActionSelectionState{
			Domain:             "macro",
			RecommendedAction:  "investigate",
			SuccessProbability: 0.79,
			GoalValue:          0.82,
			SocialCost:         0.08,
			ExpectedUtility:    0.57,
		},
		Translated: "Federal Reserve speech signaled a more hawkish balance of risks.",
		Entities:   []signal.Entity{{Name: "TLT", Type: "instrument"}},
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
		"Expectation context:",
		"expectation.action=investigate",
		"Appraisal context:",
		"appraisal.class=positive_surprise",
		"Action selection context:",
		"action.recommended=investigate",
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

func TestEnrichSignalCognitionAttachesExpectationAndAppraisal(t *testing.T) {
	sig := signal.Signal{
		ID:         "sig-2",
		Source:     "internal/desk-geo-a",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.72,
		Direction:  signal.Bullish,
		Translated: "Shipping insurers are repricing Gulf routes after renewed Iranian escalation.",
		EvidenceMeta: &evidence.Metadata{
			SourceTrust:      0.82,
			EvidenceScore:    0.79,
			FreshnessStatus:  "fresh",
			ConfidenceVector: &evidence.ConfidenceVector{FactConfidence: 0.78, NoveltyConfidence: 0.76, MarketMappingConfidence: 0.73, ExecutionConfidence: 0.69},
		},
	}

	enriched := EnrichSignalCognition(sig, "macro", &model.CollaborationInput{
		OriginDesk:             "desk-geo-a",
		OriginDomain:           "geopolitical",
		RequestedAction:        "review",
		RelationshipTrust:      0.81,
		RelationshipConfidence: 0.64,
	})

	if enriched.Expectation == nil {
		t.Fatal("expected expectation state")
	}
	if enriched.Appraisal == nil {
		t.Fatal("expected appraisal state")
	}
	if enriched.ActionSelection == nil {
		t.Fatal("expected action selection state")
	}
	if enriched.Expectation.PredictedAction == "" {
		t.Fatalf("expected predicted action, got %+v", enriched.Expectation)
	}
	if enriched.Appraisal.ViolationClass == "" {
		t.Fatalf("expected appraisal class, got %+v", enriched.Appraisal)
	}
	formatted := BuildSignalContext(enriched, SignalContextOptions{
		ContentLimit:         220,
		RelatedLimit:         4,
		EntityLimit:          8,
		IncludeEvidence:      true,
		IncludeInstitutional: true,
	})
	for _, want := range []string{
		"Expectation context:",
		"Appraisal context:",
		"Action selection context:",
		"expectation.action=",
		"appraisal.class=",
		"action.recommended=",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted context to include %q\n%s", want, formatted)
		}
	}
}

func TestBuildActionSelectionStateChoosesEscalateForHighPressureSignal(t *testing.T) {
	selection := BuildActionSelectionState(
		&model.ExpectationState{
			Domain:               "macro",
			PredictedImportance:  0.88,
			PredictedReliability: 0.81,
			PredictedTradability: 0.79,
			PredictedNovelty:     0.73,
		},
		&model.AppraisalState{
			Domain:             "macro",
			ActionPressure:     0.90,
			SocialCost:         0.12,
			RelationshipHealth: 0.80,
		},
		&model.CollaborationInput{
			RequestedAction:   "escalate",
			RelationshipTrust: 0.84,
		},
	)

	if selection == nil {
		t.Fatal("expected action selection")
	}
	if selection.RecommendedAction == "" {
		t.Fatalf("expected recommended action, got %+v", selection)
	}
	if selection.ExpectedUtility <= 0 {
		t.Fatalf("expected positive utility, got %+v", selection)
	}
}

func TestCollaborationInputForSignalLoadsPeerRelationship(t *testing.T) {
	sig := signal.Signal{
		Source: "internal/desk-geo-a",
		Raw: model.ColleagueMessage{
			ThreadID:        "thread-1",
			MessageID:       "msg-1",
			OriginDesk:      "desk-geo-a",
			OriginDomain:    "geopolitical",
			ThesisID:        "thesis-1",
			RequestedAction: "review",
			Summary:         "Iran escalation spilling into shipping lanes",
		}.Encode(),
	}

	input := CollaborationInputForSignal(sig, func(originDesk, originDomain string) (*model.DeskRelationshipBelief, bool) {
		if originDesk != "desk-geo-a" || originDomain != "geopolitical" {
			t.Fatalf("unexpected relationship lookup %s/%s", originDesk, originDomain)
		}
		return &model.DeskRelationshipBelief{
			Trust:              0.81,
			Confidence:         0.66,
			RelationshipHealth: 0.74,
			RecoveryScore:      0.19,
		}, true
	})

	if input == nil {
		t.Fatal("expected collaboration input")
	}
	if input.RelationshipTrust != 0.81 || input.RelationshipConfidence != 0.66 {
		t.Fatalf("expected peer relationship hydration, got %+v", input)
	}
	if input.RelationshipHealth != 0.74 || input.RecoveryScore != 0.19 {
		t.Fatalf("expected peer relationship health hydration, got %+v", input)
	}
}

func TestCollaborationInputForSignalLoadsAppraisalState(t *testing.T) {
	sig := signal.Signal{
		Source: "internal/desk-geo-a",
		Raw: model.ColleagueMessage{
			ThreadID:        "thread-1",
			MessageID:       "msg-1",
			OriginDesk:      "desk-geo-a",
			OriginDomain:    "geopolitical",
			ThesisID:        "thesis-1",
			RequestedAction: "review",
			Summary:         "Iran escalation spilling into shipping lanes",
		}.Encode(),
		Appraisal: &model.AppraisalState{
			ViolationClass:     "negative_surprise",
			FaceThreatScore:    0.42,
			SocialCost:         0.31,
			RelationshipHealth: 0.57,
		},
	}

	input := CollaborationInputForSignal(sig, nil)
	if input == nil {
		t.Fatal("expected collaboration input")
	}
	if input.AppraisalClass != "negative_surprise" {
		t.Fatalf("expected appraisal class, got %+v", input)
	}
	if input.FaceThreatScore != 0.42 || input.SocialCost != 0.31 || input.RelationshipHealth != 0.57 {
		t.Fatalf("expected appraisal hydration, got %+v", input)
	}
}

func TestApplyCollaborationInputAdjustsConvictionAndEvidence(t *testing.T) {
	thesis := &model.Thesis{
		Conviction: 0.70,
		Evidence:   []model.Evidence{{Source: "signal", Content: "root event", Weight: 0.6}},
	}

	ApplyCollaborationInput(thesis, &model.CollaborationInput{
		OriginDesk:             "desk-geo-a",
		Kind:                   model.ColleagueMessageProposal,
		RequestedAction:        "review",
		Summary:                "Iran shock likely to tighten shipping insurance",
		RelationshipTrust:      0.80,
		RelationshipConfidence: 0.62,
	}, 0.55, 0.18)

	if thesis.CollaborationInput == nil {
		t.Fatal("expected collaboration input to be attached")
	}
	if thesis.Conviction <= 0.70 {
		t.Fatalf("expected conviction increase, got %.2f", thesis.Conviction)
	}
	found := false
	for _, item := range thesis.Evidence {
		if item.Source == "colleague:desk-geo-a" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected colleague evidence to be attached, got %+v", thesis.Evidence)
	}
}
