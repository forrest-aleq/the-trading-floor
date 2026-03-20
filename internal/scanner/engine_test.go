package scanner

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

func TestFormatSignalIncludesCrossReferenceContext(t *testing.T) {
	formatted := formatSignal(signal.Signal{
		ID:                     "sig-1",
		Source:                 "ft",
		Type:                   signal.TypeNews,
		Category:               "corporate",
		Timestamp:              time.Now(),
		Urgency:                0.8,
		ClusterID:              "cluster-123",
		NarrativeClusterID:     "narrative-007",
		Languages:              []string{"fr"},
		TranslationProvider:    "source_payload",
		TranslationConfidence:  0.86,
		RelatedSignalIDs:       []string{"sig-a", "sig-b"},
		CorroboratingSources:   []string{"reuters", "fed-press"},
		CorroboratingEntities:  []string{"AAPL"},
		CorroboratingLanguages: []string{"en", "ar"},
		Translated:             "Apple expands India supplier footprint",
		Entities: []signal.Entity{
			{Name: "AAPL", Type: "instrument"},
		},
	})

	for _, want := range []string{
		"Cluster: cluster-123",
		"Narrative: narrative-007",
		"Original language: fr",
		"Translation: provider=source_payload confidence=0.86",
		"Related signals: 2",
		"Corroborating sources: reuters, fed-press",
		"Corroborating entities: AAPL",
		"Corroborating languages: en, ar",
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

type scannerStubClient struct {
	requests []llm.Request
}

func (s *scannerStubClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return nil, fmt.Errorf("api error (status 400): {\"error\":\"Context size has been exceeded.\"}")
	default:
		return &llm.Response{
			Content: `{"tradeable":true,"score":85,"instruments":[{"symbol":"AAPL","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.8,"category":"corporate","reasoning":"event"}`,
			Model:   "stub",
		}, nil
	}
}

func TestEvaluateRetriesCompactPromptOnContextWindowError(t *testing.T) {
	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	opp, ok := engine.Evaluate(context.Background(), signal.Signal{
		ID:        "sig-1",
		Source:    "fed-press",
		Type:      signal.TypeNews,
		Category:  "macro",
		Timestamp: time.Now(),
		Urgency:   0.9,
		Translated: strings.Repeat(
			"Federal Reserve speech on inflation and labor conditions. ",
			80,
		),
	}, "macro")
	if !ok || opp == nil {
		t.Fatal("expected compact retry to return an opportunity")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected 2 scanner requests, got %d", got)
	}
	if client.requests[0].MaxTokens != scannerMaxTokens {
		t.Fatalf("expected first request max tokens %d, got %d", scannerMaxTokens, client.requests[0].MaxTokens)
	}
	if client.requests[1].MaxTokens != scannerCompactMaxTokens {
		t.Fatalf("expected compact request max tokens %d, got %d", scannerCompactMaxTokens, client.requests[1].MaxTokens)
	}
	firstPrompt := client.requests[0].Messages[1].Content
	secondPrompt := client.requests[1].Messages[1].Content
	if len(secondPrompt) >= len(firstPrompt) {
		t.Fatalf("expected compact retry prompt to be smaller, got first=%d second=%d", len(firstPrompt), len(secondPrompt))
	}
	if opp.Direction != model.Long {
		t.Fatalf("expected long opportunity, got %s", opp.Direction)
	}
}

func TestEvaluateSkipsLowSignalSocialNoiseBeforeLLM(t *testing.T) {
	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	if _, ok := engine.Evaluate(context.Background(), signal.Signal{
		ID:         "sig-social",
		Source:     "stocktwits",
		Type:       signal.TypeSocial,
		Category:   "flows",
		Timestamp:  time.Now(),
		Urgency:    0.4,
		Entities:   []signal.Entity{{Name: "AAPL", Type: "instrument"}},
		Translated: "StockTwits mentions AAPL trending higher",
	}, "flows"); ok {
		t.Fatal("expected low-signal social chatter to be rejected without LLM")
	}
	if len(client.requests) != 0 {
		t.Fatalf("expected no LLM request for deterministic social reject, got %d", len(client.requests))
	}
}

func TestEvaluateSkipsLowIntegrityEvidenceBeforeLLM(t *testing.T) {
	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	if _, ok := engine.Evaluate(context.Background(), signal.Signal{
		ID:         "sig-evidence",
		Source:     "stocktwits",
		Type:       signal.TypeSocial,
		Category:   "flows",
		Timestamp:  time.Now(),
		Urgency:    0.8,
		Translated: "AAPL to the moon according to random posters",
		EvidenceMeta: &evidence.Metadata{
			SourceType:      "social",
			SourceTrust:     0.32,
			FreshnessStatus: "fresh",
			EvidenceScore:   0.18,
		},
	}, "flows"); ok {
		t.Fatal("expected low-integrity evidence to be rejected without LLM")
	}
	if len(client.requests) != 0 {
		t.Fatalf("expected no LLM request for evidence-gated reject, got %d", len(client.requests))
	}
}

func TestFormatSignalIncludesEvidenceContext(t *testing.T) {
	formatted := formatSignal(signal.Signal{
		ID:         "sig-1",
		Source:     "sec-edgar",
		Type:       signal.TypeFiling,
		Category:   "corporate",
		Timestamp:  time.Now(),
		Urgency:    0.9,
		Translated: "8-K filed by NVDA announcing new guidance",
		EvidenceMeta: &evidence.Metadata{
			SourceTrust:           0.95,
			SourceTier:            "primary",
			SourceType:            "primary",
			SourceDomain:          "sec.gov",
			SourceOwnerGroup:      "sec",
			OriginalLanguage:      "ar",
			TranslationProvider:   "source_payload",
			TranslationConfidence: 0.91,
			FreshnessStatus:       "fresh",
			FreshnessAgeHours:     2,
			FreshnessWindowHours:  48,
			DistinctLanguages:     2,
			ContradictionCount:    1,
			ContradictionSeverity: "medium",
			ConfidenceVector: &evidence.ConfidenceVector{
				FactConfidence:          0.94,
				NoveltyConfidence:       0.72,
				MarketMappingConfidence: 0.81,
				ExpressionConfidence:    0.78,
				ExecutionConfidence:     0.84,
				CompetenceConfidence:    0.76,
			},
			EvidenceScore: 0.91,
		},
	})

	for _, want := range []string{
		"Source trust: 0.95",
		"Source quality: tier=primary type=primary",
		"Distinct languages: 2",
		"Freshness: fresh",
		"Contradictions: 1 (medium)",
		"Evidence score: 0.91",
		"Confidence vector: fact=0.94 novelty=0.72 market_map=0.81 expression=0.78 execution=0.84 competence=0.76",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted signal missing %q\n%s", want, formatted)
		}
	}
}
