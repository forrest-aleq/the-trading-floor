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

type scannerErrorClient struct {
	requests int
	err      error
}

func (s *scannerErrorClient) Complete(_ context.Context, _ llm.Request) (*llm.Response, error) {
	s.requests++
	return nil, s.err
}

type scannerTimeoutThenSuccessClient struct {
	requests []llm.Request
}

func (s *scannerTimeoutThenSuccessClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return nil, fmt.Errorf("http request: Post \"http://127.0.0.1:1234/v1/chat/completions\": context deadline exceeded")
	}
	return &llm.Response{
		Content: `{"tradeable":true,"score":81,"instruments":[{"symbol":"AAPL","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.6,"category":"corporate","reasoning":"earnings catalyst with clean setup"}`,
		Model:   "stub",
	}, nil
}

type scannerCompilerFallbackClient struct {
	requests []llm.Request
}

func (s *scannerCompilerFallbackClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &llm.Response{
			Content: "Thinking Process:\n1. This looks actionable but the model forgot the final block.",
			Model:   "scanner",
		}, nil
	}
	if req.Model == "gemma-the-writer-mighty-sword-9b" {
		return &llm.Response{
			Content: `{"tradeable":true,"score":84,"instruments":[{"symbol":"TLT","sec_type":"STK","currency":"USD"}],"direction":"short","urgency":0.7,"category":"macro","reasoning":"rates repricing after hawkish surprise"}`,
			Model:   "compiler",
		}, nil
	}
	return &llm.Response{
		Content: "<think>\nStill no final block.\n",
		Model:   "scanner",
	}, nil
}

type scannerThoughtTimeoutThenStructuredFallbackClient struct {
	requests []llm.Request
}

func (s *scannerThoughtTimeoutThenStructuredFallbackClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if !req.JSONMode {
		return nil, fmt.Errorf("http request: Post \"http://127.0.0.1:1234/v1/chat/completions\": context deadline exceeded")
	}
	return &llm.Response{
		Content: `{"tradeable":true,"score":83,"instruments":[{"symbol":"NVDA","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.9,"category":"corporate","reasoning":"earnings beat and guidance raise with clean confirmation"}`,
		Model:   "stub",
	}, nil
}

type scannerThoughtParseThenStructuredFallbackClient struct {
	requests []llm.Request
}

func (s *scannerThoughtParseThenStructuredFallbackClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if !req.JSONMode {
		return &llm.Response{
			Content: "<think>\nThis looks interesting but the model forgot the final block.\n",
			Model:   "qwen",
		}, nil
	}
	return &llm.Response{
		Content: `{"tradeable":true,"score":79,"instruments":[{"symbol":"USO","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.7,"category":"macro","reasoning":"oil supply shock setup"}`,
		Model:   "stub",
	}, nil
}

type scannerBlankInstrumentClient struct {
	requests []llm.Request
}

func (s *scannerBlankInstrumentClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: `{"tradeable":true,"score":84,"instruments":[{"symbol":"","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.7,"category":"macro","reasoning":"missing instrument should be rejected"}`,
		Model:   "stub",
	}, nil
}

type scannerDeterministicClient struct {
	requests int
	content  string
}

func (s *scannerDeterministicClient) Complete(_ context.Context, _ llm.Request) (*llm.Response, error) {
	s.requests++
	return &llm.Response{
		Content: s.content,
		Model:   "stub",
	}, nil
}

func TestEvaluateRetriesCompactPromptOnContextWindowError(t *testing.T) {
	t.Setenv("SCANNER_RESPONSE_MODE", "json")

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

func TestDetectScannerResponseModeUsesStructuredForLocalQwen(t *testing.T) {
	t.Setenv("SCANNER_RESPONSE_MODE", "")
	t.Setenv("LLM_BASE_URL", "http://127.0.0.1:11434/v1")
	if got := detectScannerResponseMode("qwen3:8b"); got != scannerResponseModeStructured {
		t.Fatalf("expected local qwen scanner to prefer structured mode, got %s", got)
	}
}

func TestDetectScannerResponseModeKeepsThoughtModeForRemoteQwen(t *testing.T) {
	t.Setenv("SCANNER_RESPONSE_MODE", "")
	t.Setenv("LLM_BASE_URL", "https://openrouter.ai/api/v1")
	if got := detectScannerResponseMode("qwen/qwen3.5-9b"); got != scannerResponseModeThought {
		t.Fatalf("expected remote qwen scanner to keep thought mode, got %s", got)
	}
}

func TestEvaluateDetailedCachesBySignalAndDomain(t *testing.T) {
	t.Setenv("SCANNER_RESPONSE_MODE", "json")

	client := &scannerDeterministicClient{
		content: `{"tradeable":true,"score":84,"instruments":[{"symbol":"TLT","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.7,"category":"macro","reasoning":"rates setup"}`,
	}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	sig := signal.Signal{
		ID:         "sig-cache-1",
		Source:     "fed-press",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.7,
		Translated: "Fed signals tighter conditions.",
	}

	first := engine.EvaluateDetailed(context.Background(), sig, "macro")
	second := engine.EvaluateDetailed(context.Background(), sig, "macro")

	if !first.Accepted || first.Opportunity == nil {
		t.Fatalf("expected first evaluation accepted, got %+v", first)
	}
	if !second.Accepted || second.Opportunity == nil {
		t.Fatalf("expected second evaluation accepted, got %+v", second)
	}
	if client.requests != 1 {
		t.Fatalf("expected exactly one LLM request, got %d", client.requests)
	}
	if first.Opportunity.ID == second.Opportunity.ID {
		t.Fatalf("expected unique opportunity IDs after cache clone, got %q", first.Opportunity.ID)
	}
}

func TestEvaluateDetailedCacheKeyIncludesInstitutionalState(t *testing.T) {
	t.Setenv("SCANNER_RESPONSE_MODE", "json")

	client := &scannerDeterministicClient{
		content: `{"tradeable":false,"score":40,"instruments":[],"direction":"long","urgency":0.3,"category":"macro","reasoning":"ignore"}`,
	}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	base := signal.Signal{
		ID:         "sig-cache-2",
		Source:     "internal/desk-geo-a",
		Type:       signal.TypeAlternative,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.6,
		Translated: "Internal signal",
	}

	sigA := base
	sigA.InstitutionalContext = "Institutional context:\n  colleague.from_desk=desk-geo-a\n  colleague.peer_trust=0.70"
	sigB := base
	sigB.InstitutionalContext = "Institutional context:\n  colleague.from_desk=desk-geo-a\n  colleague.peer_trust=0.90"

	engine.EvaluateDetailed(context.Background(), sigA, "macro")
	engine.EvaluateDetailed(context.Background(), sigB, "macro")

	if client.requests != 2 {
		t.Fatalf("expected distinct institutional contexts to miss shared cache, got %d requests", client.requests)
	}
}

func TestEvaluateUsesThoughtModeForQwenScanner(t *testing.T) {
	t.Setenv("SCANNER_MODEL", "qwen/qwen3.5-9b")

	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	_, _ = engine.Evaluate(context.Background(), signal.Signal{
		ID:         "sig-thought",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.8,
		Translated: "Central bank communication shifted meaningfully toward renewed tightening.",
	}, "macro")

	if len(client.requests) == 0 {
		t.Fatal("expected scanner request")
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected Qwen scanner to avoid strict JSON mode")
	}
	if got := client.requests[0].Messages[0].Content; !strings.Contains(got, "FINAL_DECISION") {
		t.Fatalf("expected thought-mode scanner prompt, got %q", got)
	}
}

func TestEvaluateUsesStructuredJSONForNonQwenScanner(t *testing.T) {
	t.Setenv("SCANNER_MODEL", "google/gemma-3-27b")

	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	_, _ = engine.Evaluate(context.Background(), signal.Signal{
		ID:         "sig-json",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.8,
		Translated: "Central bank communication shifted meaningfully toward renewed tightening.",
	}, "macro")

	if len(client.requests) == 0 {
		t.Fatal("expected scanner request")
	}
	if !client.requests[0].JSONMode {
		t.Fatal("expected non-Qwen scanner to stay in strict JSON mode")
	}
	if got := client.requests[0].Messages[0].Content; !strings.Contains(got, "Output one final JSON object only") {
		t.Fatalf("expected structured scanner prompt, got %q", got)
	}
}

func TestEvaluateRetriesCompactPromptOnTimeout(t *testing.T) {
	t.Setenv("SCANNER_RESPONSE_MODE", "json")

	client := &scannerTimeoutThenSuccessClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	opp, ok := engine.Evaluate(context.Background(), signal.Signal{
		ID:         "sig-timeout",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "corporate",
		Timestamp:  time.Now(),
		Urgency:    0.8,
		Translated: strings.Repeat("Earnings guidance revision. ", 60),
	}, "corporate")
	if !ok || opp == nil {
		t.Fatal("expected compact retry after timeout to recover an opportunity")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected 2 scanner requests, got %d", got)
	}
	if len(client.requests[1].Messages[1].Content) >= len(client.requests[0].Messages[1].Content) {
		t.Fatalf("expected compact retry prompt to be smaller, got first=%d second=%d", len(client.requests[0].Messages[1].Content), len(client.requests[1].Messages[1].Content))
	}
}

func TestEvaluateFallsBackToCompilerWhenThoughtModeMissesFinalBlock(t *testing.T) {
	t.Setenv("SCANNER_MODEL", "qwen/qwen3.5-9b")
	t.Setenv("SCANNER_COMPILER_MODEL", "gemma-the-writer-mighty-sword-9b")

	client := &scannerCompilerFallbackClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	opp, ok := engine.Evaluate(context.Background(), signal.Signal{
		ID:         "sig-compiler",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.8,
		Translated: "Markets reprice after hawkish central bank surprise.",
	}, "macro")
	if !ok || opp == nil {
		t.Fatal("expected compiler fallback to recover a structured decision")
	}
	if got := len(client.requests); got != 3 {
		t.Fatalf("expected thought pass, structured retry, and compiler pass, got %d requests", got)
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected initial Qwen thought pass to avoid strict JSON mode")
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected structured retry to force strict JSON mode")
	}
	if !client.requests[2].JSONMode {
		t.Fatal("expected compiler pass to force strict JSON mode")
	}
	if client.requests[2].Model != "gemma-the-writer-mighty-sword-9b" {
		t.Fatalf("unexpected compiler model %q", client.requests[2].Model)
	}
	if opp.Direction != model.Short || len(opp.Instruments) != 1 || opp.Instruments[0].Symbol != "TLT" {
		t.Fatalf("unexpected compiler fallback opportunity: %+v", opp)
	}
}

func TestEvaluateFallsBackToStructuredJSONAfterThoughtTimeouts(t *testing.T) {
	t.Setenv("SCANNER_MODEL", "qwen/qwen3.5-9b")

	client := &scannerThoughtTimeoutThenStructuredFallbackClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	opp, ok := engine.Evaluate(context.Background(), signal.Signal{
		ID:         "sig-thought-timeout",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "corporate",
		Timestamp:  time.Now(),
		Urgency:    0.95,
		Translated: "NVIDIA beats earnings, raises guidance, and says AI backlog expanded materially into next quarter.",
	}, "corporate")
	if !ok || opp == nil {
		t.Fatal("expected structured fallback to recover an opportunity")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected one thought attempt plus one structured fallback, got %d", got)
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected initial Qwen attempt to stay in thought mode")
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected final scanner fallback to force structured JSON mode")
	}
	if opp.Instruments[0].Symbol != "NVDA" {
		t.Fatalf("unexpected structured fallback opportunity: %+v", opp)
	}
}

func TestEvaluateFallsBackToStructuredJSONAfterThoughtParseFailure(t *testing.T) {
	t.Setenv("SCANNER_MODEL", "qwen/qwen3.5-9b")

	client := &scannerThoughtParseThenStructuredFallbackClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	opp, ok := engine.Evaluate(context.Background(), signal.Signal{
		ID:         "sig-thought-parse-fail",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.8,
		Translated: "Energy ministers warn of a near-term supply disruption after regional escalation.",
	}, "macro")
	if !ok || opp == nil {
		t.Fatal("expected structured fallback after parse failure to recover an opportunity")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected one thought attempt plus one structured fallback, got %d", got)
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected final scanner fallback to force structured JSON mode")
	}
	if opp.Instruments[0].Symbol != "USO" {
		t.Fatalf("unexpected structured fallback opportunity: %+v", opp)
	}
}

func TestEvaluateSkipsLowSignalSocialNoiseBeforeLLM(t *testing.T) {
	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	result := engine.EvaluateDetailed(context.Background(), signal.Signal{
		ID:         "sig-social",
		Source:     "stocktwits",
		Type:       signal.TypeSocial,
		Category:   "flows",
		Timestamp:  time.Now(),
		Urgency:    0.4,
		Entities:   []signal.Entity{{Name: "AAPL", Type: "instrument"}},
		Translated: "StockTwits mentions AAPL trending higher",
	}, "flows")
	if result.Accepted {
		t.Fatal("expected low-signal social chatter to be rejected without LLM")
	}
	if result.Reason != "prefilter:low_signal_social_noise" {
		t.Fatalf("unexpected reject reason %q", result.Reason)
	}
	if len(client.requests) != 0 {
		t.Fatalf("expected no LLM request for deterministic social reject, got %d", len(client.requests))
	}
}

func TestEvaluateSkipsLowIntegrityEvidenceBeforeLLM(t *testing.T) {
	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	result := engine.EvaluateDetailed(context.Background(), signal.Signal{
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
	}, "flows")
	if result.Accepted {
		t.Fatal("expected low-integrity evidence to be rejected without LLM")
	}
	if !strings.HasPrefix(result.Reason, "prefilter:") {
		t.Fatalf("expected prefilter reject reason, got %q", result.Reason)
	}
	if len(client.requests) != 0 {
		t.Fatalf("expected no LLM request for evidence-gated reject, got %d", len(client.requests))
	}
}

func TestEvaluateDetailedReportsScoreThresholdReject(t *testing.T) {
	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 90)

	result := engine.EvaluateDetailed(context.Background(), signal.Signal{
		ID:         "sig-threshold",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.9,
		Translated: "Federal Reserve speech on inflation and labor conditions.",
	}, "macro")
	if result.Accepted {
		t.Fatal("expected threshold reject")
	}
	if result.Reason != "score_below_threshold" {
		t.Fatalf("unexpected reject reason %q", result.Reason)
	}
	if result.Score != 85 {
		t.Fatalf("expected score to be surfaced, got %.2f", result.Score)
	}
}

func TestEvaluateDetailedUsesReplayEvaluationTimeForStaleness(t *testing.T) {
	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)
	ts := time.Now().Add(-72 * time.Hour)

	ctx := WithEvaluationTime(context.Background(), ts)
	result := engine.EvaluateDetailed(ctx, signal.Signal{
		ID:         "sig-replay-fresh",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  ts,
		Urgency:    0.9,
		Translated: "Federal Reserve speech on inflation and labor conditions.",
	}, "macro")
	if !result.Accepted || result.Opportunity == nil {
		t.Fatalf("expected replay-time evaluation to bypass stale reject, got %+v", result)
	}
}

func TestEvaluateDetailedRejectsBlankInstruments(t *testing.T) {
	client := &scannerStubClient{}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	client.requests = nil
	client2 := &scannerBlankInstrumentClient{}
	engine = NewEngine(llm.NewRouter(client2, client2, client2), 70)

	result := engine.EvaluateDetailed(context.Background(), signal.Signal{
		ID:         "sig-blank-inst",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.9,
		Translated: "Central bank communication shifted meaningfully toward renewed tightening.",
	}, "macro")
	if result.Accepted {
		t.Fatalf("expected blank instrument response to be rejected, got %+v", result)
	}
	if result.Reason != "no_instruments" {
		t.Fatalf("unexpected reject reason %q", result.Reason)
	}
}

func TestEvaluateTripsCooldownOnUnavailableLLM(t *testing.T) {
	client := &scannerErrorClient{err: fmt.Errorf("http request: Post \"http://127.0.0.1:1234/v1/chat/completions\": dial tcp 127.0.0.1:1234: connect: connection refused")}
	engine := NewEngine(llm.NewRouter(client, client, client), 70)

	sig := signal.Signal{
		ID:         "sig-llm-down",
		Source:     "ft",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.9,
		Translated: "Bank of England surprises markets with emergency statement.",
	}

	if _, ok := engine.Evaluate(context.Background(), sig, "macro"); ok {
		t.Fatal("expected unavailable LLM backend to reject signal")
	}
	if _, ok := engine.Evaluate(context.Background(), sig, "macro"); ok {
		t.Fatal("expected cooldown to reject signal without another LLM call")
	}
	if client.requests != 1 {
		t.Fatalf("expected cooldown to suppress follow-up LLM call, got %d requests", client.requests)
	}
}

func TestEvaluateParsesThoughtfulTerminalDecisionBlock(t *testing.T) {
	result, err := parseScanResponse(`Thinking Process:

1. This is meaningful macro news.
2. It still lacks a precise directional setup.

FINAL_DECISION
tradeable: true
score: 82
instruments: TLT:ETF:USD, XLF:STK:USD
direction: short
urgency: 0.67
category: macro
reasoning: tighter liquidity pressures long duration risk assets
END_FINAL_DECISION`)
	if err != nil {
		t.Fatalf("expected structured decision block to parse, got %v", err)
	}
	if !result.Tradeable || result.Score != 82 {
		t.Fatalf("unexpected parsed decision: %+v", result)
	}
	if result.Direction != "short" || result.Category != "macro" {
		t.Fatalf("unexpected parsed direction/category: %+v", result)
	}
	if len(result.Instruments) != 2 {
		t.Fatalf("expected 2 parsed instruments, got %+v", result.Instruments)
	}
}

func TestEvaluateParsesCaseInsensitiveDecisionBlock(t *testing.T) {
	result, err := parseScanResponse(`thinking process

Final_Decision
tradeable: false
score: 18
instruments: none
direction: none
urgency: 0.10
category: macro
reasoning: low signal
End_Final_Decision`)
	if err != nil {
		t.Fatalf("expected mixed-case decision block to parse, got %v", err)
	}
	if result.Tradeable {
		t.Fatalf("expected non-tradeable decision, got %+v", result)
	}
	if result.Direction != "none" || len(result.Instruments) != 0 {
		t.Fatalf("unexpected parsed decision: %+v", result)
	}
}

func TestParseScanResponseRecoversConservativeRejectFromIncompleteThought(t *testing.T) {
	result, err := parseScanResponse(`Thinking Process:

1. Analyze the signal.
2. It does not meet the criteria because there is no clear catalyst.
3. No exact instrument is justified from the text.

Domain filter: macro
`)
	if err != nil {
		t.Fatalf("expected conservative reject recovery, got %v", err)
	}
	if result.Tradeable {
		t.Fatalf("expected recovered reject, got %+v", result)
	}
	if result.Category != "macro" {
		t.Fatalf("expected inferred macro category, got %+v", result)
	}
	if !strings.Contains(result.Reasoning, "no clear catalyst") {
		t.Fatalf("expected recovered reasoning, got %+v", result)
	}
}

func TestParseScanResponseRecoversConservativeRejectFromThinkTrace(t *testing.T) {
	result, err := parseScanResponse(`<think>
Okay, let's break this down. The user wants a JSON output for a macro signal.
The event is real, but there is no exact instrument and no clear directional setup.
</think>`)
	if err != nil {
		t.Fatalf("expected conservative reject recovery, got %v", err)
	}
	if result.Tradeable {
		t.Fatalf("expected recovered reject, got %+v", result)
	}
	if !strings.Contains(strings.ToLower(result.Reasoning), "no exact instrument") {
		t.Fatalf("expected recovered reasoning, got %+v", result)
	}
}

func TestParseScanResponseDoesNotSilentlyRejectPositiveThoughtTrace(t *testing.T) {
	_, err := parseScanResponse(`Thinking Process:

1. This signal is tradeable.
2. There is a clear actionable trade and it meets all criteria.
3. The model forgot to emit the final block.
`)
	if err == nil {
		t.Fatal("expected positive incomplete thought trace to remain a parse error")
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
