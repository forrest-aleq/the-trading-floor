package scanner

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
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
