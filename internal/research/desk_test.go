package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/marketcontext"
	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type researchMarketContextStub struct {
	price  float64
	change map[time.Duration]float64
}

func (s researchMarketContextStub) LatestPrice(model.Instrument) (float64, bool) {
	return s.price, s.price > 0
}

func (s researchMarketContextStub) PriceChange(_ model.Instrument, window time.Duration) (float64, bool) {
	value, ok := s.change[window]
	return value, ok
}

func (s researchMarketContextStub) BestEffortPrice(_ context.Context, inst model.Instrument) (model.Instrument, float64, bool) {
	if s.price <= 0 {
		return model.Instrument{}, 0, false
	}
	resolved := inst
	if resolved.SecType == "STK" && inst.Symbol == "VIX" {
		resolved.SecType = "IND"
		resolved.Exchange = "CBOE"
	}
	return resolved, s.price, true
}

type researchStubClient struct {
	requests []llm.Request
}

func (s *researchStubClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: validResearchJSON(),
		Model:   "stub",
	}, nil
}

type researchCompilerFallbackClient struct {
	requests []llm.Request
}

func (s *researchCompilerFallbackClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &llm.Response{
			Content: "Thinking Process:\n1. Strong event setup.\n2. Clear catalyst.\n3. The model forgot to emit JSON.",
			Model:   "analysis",
		}, nil
	}
	if req.Model == "gemma-the-writer-mighty-sword-9b" && strings.Contains(req.Messages[0].Content, "trading thesis compiler") {
		return &llm.Response{
			Content: validResearchJSON(),
			Model:   "compiler",
		}, nil
	}
	return &llm.Response{
		Content: "Still thinking without final JSON.",
		Model:   "analysis",
	}, nil
}

type researchTerminalBlockClient struct {
	requests []llm.Request
}

func (s *researchTerminalBlockClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: "Thinking Process:\n1. Strong event setup.\n2. Clear catalyst.\nFINAL_JSON\n" + validResearchJSON() + "\nEND_FINAL_JSON",
		Model:   "analysis",
	}, nil
}

type researchStructuredRetryClient struct {
	requests []llm.Request
}

func (s *researchStructuredRetryClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if !req.JSONMode {
		return &llm.Response{
			Content: "Thinking Process:\n1. Strong event setup.\n2. Clear catalyst.\n3. The model forgot to emit JSON.",
			Model:   "analysis",
		}, nil
	}
	return &llm.Response{
		Content: validResearchJSON(),
		Model:   "analysis-json",
	}, nil
}

type researchPrimaryErrorRetryClient struct {
	requests []llm.Request
}

func (s *researchPrimaryErrorRetryClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return nil, errors.New("context deadline exceeded")
	}
	return &llm.Response{
		Content: validResearchJSON(),
		Model:   "analysis-json",
	}, nil
}

func TestInvestigateUsesThoughtModeForQwenResearch(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")
	t.Setenv("RESEARCH_RESPONSE_MODE", "thought")

	client := &researchStubClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected thesis, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected non-nil thesis")
	}
	if len(client.requests) == 0 {
		t.Fatal("expected research request")
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected Qwen research request to avoid strict JSON mode")
	}
	if got := client.requests[0].Messages[0].Content; got == desk.systemPrompt {
		t.Fatal("expected thought-friendly research prompt prefix")
	}
	if got := client.requests[0].Messages[0].Content; !containsTerminalContract(got) {
		t.Fatalf("expected terminal JSON contract in research prompt, got %q", got)
	}
	if got := client.requests[0].Messages[1].Content; !strings.Contains(got, "Federal Reserve speech signaled a more hawkish balance of risks.") {
		t.Fatalf("expected research prompt to include signal content, got %q", got)
	}
}

type researchMissingConvictionClient struct {
	requests []llm.Request
}

func (s *researchMissingConvictionClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: `{
  "structure": "single",
  "instrument": {"symbol": "TLT", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
  "legs": [],
  "direction": "long",
  "entry_price": 90.5,
  "target_price": 96.0,
  "stop_loss": 88.0,
  "time_horizon_hours": 48,
  "position_size_pct": 0.01,
  "evidence": ["hawkish policy rhetoric"],
  "counter_args": ["positioning may already be crowded"],
  "kill_rules": [{"condition": "price_below_stop", "threshold": 88.0, "action": "close"}],
  "reasoning": "hawkish repricing favors duration rebound after overshoot"
}`,
		Model: "analysis",
	}, nil
}

type researchMissingPositionSizeClient struct {
	requests []llm.Request
}

func (s *researchMissingPositionSizeClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: `{
  "structure": "single",
  "instrument": {"symbol": "TLT", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
  "legs": [],
  "direction": "long",
  "entry_price": 90.5,
  "target_price": 96.0,
  "stop_loss": 88.0,
  "conviction": 0.76,
  "time_horizon_hours": 48,
  "evidence": ["hawkish policy rhetoric"],
  "counter_args": ["positioning may already be crowded"],
  "kill_rules": [{"condition": "price_below_stop", "threshold": 88.0, "action": "close"}],
  "reasoning": "hawkish repricing favors duration rebound after overshoot"
}`,
		Model: "analysis",
	}, nil
}

type researchStringNumericClient struct {
	requests []llm.Request
}

func (s *researchStringNumericClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	s.requests = append(s.requests, req)
	return &llm.Response{
		Content: `{
  "structure": "single",
  "instrument": {"symbol": "TLT 2026-05-18 130PUT", "sec_type": "STK", "currency": "USD", "exchange": "SMART", "expiry": "", "strike": "130", "right": "PUT"},
  "legs": [],
  "direction": "long",
  "entry_price": "2.75",
  "target_price": "4.50",
  "stop_loss": "1.80",
  "conviction": "0.76",
  "time_horizon_hours": "48",
  "position_size_pct": "0.01",
  "strategy": "macro",
  "surprise_assessment": {
    "truth_score": "0.8",
    "novelty_score": "0.7",
    "priced_in_score": "0.3",
    "reaction_gap_score": "0.6",
    "unmoved_asset_score": "0.5",
    "summary": "rates repricing is incomplete"
  },
  "evidence": ["hawkish policy rhetoric"],
  "counter_args": ["positioning may already be crowded"],
  "kill_rules": [{"condition": "price_below_stop", "threshold": "1.8", "action": "close"}],
  "reasoning": "hawkish repricing favors duration rebound after overshoot"
}`,
		Model: "analysis",
	}, nil
}

func TestInvestigateCompilerFallbackRecoversStructuredThesis(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")
	t.Setenv("RESEARCH_RESPONSE_MODE", "thought")
	t.Setenv("RESEARCH_COMPILER_MODEL", "gemma-the-writer-mighty-sword-9b")

	client := &researchCompilerFallbackClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected compiler fallback to recover thesis, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis from compiler fallback")
	}
	if got := len(client.requests); got != 3 {
		t.Fatalf("expected analysis call, structured retry, plus compiler call, got %d", got)
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected initial research call to avoid strict JSON mode")
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected structured retry to use strict JSON mode")
	}
	if !client.requests[2].JSONMode {
		t.Fatal("expected compiler request to use strict JSON mode")
	}
	if client.requests[2].Model != "gemma-the-writer-mighty-sword-9b" {
		t.Fatalf("unexpected compiler model %q", client.requests[2].Model)
	}
}

func TestInvestigateAcceptsTerminalJSONBlockWithoutCompilerFallback(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")
	t.Setenv("RESEARCH_RESPONSE_MODE", "thought")

	client := &researchTerminalBlockClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected terminal JSON block to parse, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis")
	}
	if got := len(client.requests); got != 1 {
		t.Fatalf("expected only one research call, got %d", got)
	}
}

func TestInvestigateStructuredRetryRecoversBeforeCompilerFallback(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")
	t.Setenv("RESEARCH_RESPONSE_MODE", "thought")

	client := &researchStructuredRetryClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected structured retry to recover thesis, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis from structured retry")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected analysis call plus structured retry, got %d", got)
	}
	if client.requests[0].JSONMode {
		t.Fatal("expected initial thought-mode request")
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected structured retry request")
	}
	if got := client.requests[1].Messages[1].Content; !strings.Contains(got, "Signal snapshot") {
		t.Fatalf("expected structured retry to retain compact signal context, got %q", got)
	}
}

func TestInvestigateDetailedMarksLowConvictionAsReject(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")

	client := &researchStubClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.8)

	result, err := desk.InvestigateDetailed(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected no hard error, got %v", err)
	}
	if result.Thesis == nil {
		t.Fatal("expected thesis draft")
	}
	if result.Accepted {
		t.Fatal("expected low-conviction thesis to be rejected")
	}
	if result.Reason != "conviction_below_threshold" {
		t.Fatalf("unexpected reject reason %q", result.Reason)
	}
}

func TestInvestigateRecoversWithStructuredRetryAfterPrimaryError(t *testing.T) {
	t.Setenv("RESEARCH_MODEL", "qwen/qwen3.5-35b-a3b")
	t.Setenv("RESEARCH_RESPONSE_MODE", "thought")
	t.Setenv("RESEARCH_COMPILER_MODEL", "gemma-the-writer-mighty-sword-9b")

	client := &researchPrimaryErrorRetryClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected retry to recover primary error, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis from structured retry")
	}
	if got := len(client.requests); got != 2 {
		t.Fatalf("expected primary request plus retry, got %d", got)
	}
	if !client.requests[1].JSONMode {
		t.Fatal("expected retry request to use strict JSON mode")
	}
	if client.requests[1].Tier != llm.TierSpeed {
		t.Fatalf("expected retry to downgrade to speed tier, got %v", client.requests[1].Tier)
	}
}

func TestInvestigateDefaultsMissingConvictionAndStrategy(t *testing.T) {
	client := &researchMissingConvictionClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected thesis with derived conviction, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis")
	}
	if thesis.Conviction != 0.78 {
		t.Fatalf("expected conviction to derive from opportunity score, got %.2f", thesis.Conviction)
	}
	if thesis.Strategy != "event" {
		t.Fatalf("expected default event strategy, got %q", thesis.Strategy)
	}
}

func TestInvestigateDefaultsMissingPositionSize(t *testing.T) {
	t.Setenv("RESEARCH_DEFAULT_POSITION_SIZE_PCT", "0.015")
	ReloadRuntimeConfig()
	defer ReloadRuntimeConfig()

	client := &researchMissingPositionSizeClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected thesis with default position size, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis")
	}
	if thesis.PositionSize != 0.015 {
		t.Fatalf("expected default position size 0.015, got %.4f", thesis.PositionSize)
	}
}

func TestInvestigateAcceptsStringEncodedNumericFields(t *testing.T) {
	client := &researchStringNumericClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected thesis, got %v", err)
	}
	if thesis.PrimaryInstrument().SecType != "OPT" {
		t.Fatalf("expected normalized option instrument, got %q", thesis.PrimaryInstrument().SecType)
	}
	if thesis.PrimaryInstrument().Strike != 130 {
		t.Fatalf("expected parsed strike 130, got %.2f", thesis.PrimaryInstrument().Strike)
	}
	if thesis.EntryPrice != 2.75 {
		t.Fatalf("expected parsed entry price 2.75, got %.2f", thesis.EntryPrice)
	}
	if thesis.TimeHorizon != 48*time.Hour {
		t.Fatalf("expected 48h time horizon, got %s", thesis.TimeHorizon)
	}
}

func TestHydrateThesisPricingUsesResolvedContextPrice(t *testing.T) {
	client := &researchMissingPositionSizeClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)
	desk.SetMarketContextService(marketcontext.NewService(researchMarketContextStub{
		price: 18.4,
		change: map[time.Duration]float64{
			15 * time.Minute: 0.8,
		},
	}))

	thesis := &model.Thesis{
		Instrument: model.Instrument{
			Symbol:   "VIX",
			SecType:  "STK",
			Currency: "USD",
			Exchange: "SMART",
		},
	}

	desk.HydrateThesisPricing(context.Background(), thesis)
	if thesis.EntryPrice != 18.4 {
		t.Fatalf("expected hydrated entry price 18.4, got %.2f", thesis.EntryPrice)
	}
	if thesis.MarketContext == nil || thesis.MarketContext.CurrentPrice != 18.4 {
		t.Fatalf("expected hydrated market context, got %+v", thesis.MarketContext)
	}
}

func TestEnrichResearchJSONBackfillsOpportunityAndMarketContext(t *testing.T) {
	raw := `{
  "structure": "single",
  "instrument": {},
  "direction": "",
  "entry_price": 0,
  "target_price": 96.0
}`

	opp := testOpportunity()
	marketCtx := &model.MarketContext{CurrentPrice: 91.25}

	enriched := enrichResearchJSON(raw, opp, marketCtx)

	var payload map[string]any
	if err := json.Unmarshal([]byte(enriched), &payload); err != nil {
		t.Fatalf("expected valid JSON after enrichment, got %v", err)
	}

	instrument, ok := payload["instrument"].(map[string]any)
	if !ok {
		t.Fatalf("expected instrument payload, got %#v", payload["instrument"])
	}
	if got := instrument["symbol"]; got != "TLT" {
		t.Fatalf("expected symbol TLT, got %#v", got)
	}
	if got := payload["direction"]; got != "long" {
		t.Fatalf("expected long direction, got %#v", got)
	}
	entryPrice, ok := numericValue(payload["entry_price"])
	if !ok {
		t.Fatalf("expected numeric entry price, got %#v", payload["entry_price"])
	}
	if entryPrice != 91.25 {
		t.Fatalf("expected market-context entry price 91.25, got %.2f", entryPrice)
	}
}

func TestEnrichResearchJSONBackfillsNullDirection(t *testing.T) {
	raw := `{
  "structure": "single",
  "instrument": {},
  "direction": null
}`

	opp := testOpportunity()
	enriched := enrichResearchJSON(raw, opp, nil)

	var payload map[string]any
	if err := json.Unmarshal([]byte(enriched), &payload); err != nil {
		t.Fatalf("expected valid JSON after enrichment, got %v", err)
	}
	if got := payload["direction"]; got != "long" {
		t.Fatalf("expected null direction to backfill from opportunity, got %#v", got)
	}
}

func TestInvestigateAllowsMissingEntryPriceWhenDirectionAndInstrumentExist(t *testing.T) {
	client := &researchMissingEntryPriceClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	thesis, err := desk.Investigate(context.Background(), testOpportunity(), testSignal(), "macro-rates-a")
	if err != nil {
		t.Fatalf("expected thesis without explicit entry price, got %v", err)
	}
	if thesis == nil {
		t.Fatal("expected thesis")
	}
	if thesis.EntryPrice != 0 {
		t.Fatalf("expected missing entry price to remain zero without market context, got %.2f", thesis.EntryPrice)
	}
	if thesis.Direction != model.Long {
		t.Fatalf("expected normalized long direction, got %s", thesis.Direction)
	}
}

func TestNormalizeResearchInstrumentParsesOptionContractSymbol(t *testing.T) {
	inst := normalizeResearchInstrument(model.Instrument{
		Symbol:   "TLT 2026-05-18 130PUT",
		SecType:  "STK",
		Currency: "USD",
		Exchange: "SMART",
	})

	if inst.Symbol != "TLT" {
		t.Fatalf("expected underlying symbol TLT, got %q", inst.Symbol)
	}
	if inst.SecType != "OPT" {
		t.Fatalf("expected sec type OPT, got %q", inst.SecType)
	}
	if inst.Expiry != "20260518" {
		t.Fatalf("expected normalized expiry 20260518, got %q", inst.Expiry)
	}
	if inst.Strike != 130 {
		t.Fatalf("expected strike 130, got %.2f", inst.Strike)
	}
	if inst.Right != "P" {
		t.Fatalf("expected right P, got %q", inst.Right)
	}
	if inst.Multiplier != "100" {
		t.Fatalf("expected multiplier 100, got %q", inst.Multiplier)
	}
}

type researchMissingEntryPriceClient struct{}

func (c *researchMissingEntryPriceClient) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Content: `{
  "instrument": {"symbol": "TLT", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
  "direction": "long",
  "target_price": 96.0,
  "stop_loss": 88.0,
  "conviction": 0.74,
  "time_horizon_hours": 24,
  "position_size_pct": 0.01,
  "strategy": "event",
  "evidence": ["macro spillover"],
  "counter_args": ["positioning"],
  "kill_rules": [{"condition": "price_below_stop", "threshold": 88.0, "action": "close"}],
  "reasoning": "still valid without explicit entry price"
}`,
		Model: "analysis",
	}, nil
}

func TestNormalizeResearchInstrumentDowngradesStaleDerivativeToUnderlying(t *testing.T) {
	t.Setenv("RESEARCH_STALE_DERIVATIVE_POLICY", "")

	inst := normalizeResearchInstrument(model.Instrument{
		Symbol:   "TLT 2023-06-16 125PUT",
		SecType:  "STK",
		Currency: "USD",
		Exchange: "SMART",
	})

	if inst.Symbol != "TLT" {
		t.Fatalf("expected underlying symbol TLT, got %q", inst.Symbol)
	}
	if inst.SecType != "STK" {
		t.Fatalf("expected stale derivative fallback to STK, got %q", inst.SecType)
	}
	if inst.Expiry != "" || inst.Right != "" || inst.Strike != 0 {
		t.Fatalf("expected stale derivative fields to be cleared, got %+v", inst)
	}
}

func TestNormalizeResearchInstrumentExtractsUnderlyingFromMalformedOptionSymbol(t *testing.T) {
	t.Setenv("RESEARCH_STALE_DERIVATIVE_POLICY", "")

	inst := normalizeResearchInstrument(model.Instrument{
		Symbol:   "QQQ385C20231020 20231020 385C",
		SecType:  "STK",
		Currency: "USD",
		Exchange: "SMART",
	})

	if inst.Symbol != "QQQ" {
		t.Fatalf("expected underlying symbol QQQ, got %q", inst.Symbol)
	}
	if inst.SecType != "STK" {
		t.Fatalf("expected malformed stale option to downgrade to STK, got %q", inst.SecType)
	}
}

func TestNormalizeResearchInstrumentDowngradesIncompleteDerivativeLikeSymbolToUnderlying(t *testing.T) {
	inst := normalizeResearchInstrument(model.Instrument{
		Symbol:   "VIX 25C",
		SecType:  "STK",
		Currency: "USD",
		Exchange: "SMART",
	})

	if inst.Symbol != "VIX" {
		t.Fatalf("expected underlying symbol VIX, got %q", inst.Symbol)
	}
	if inst.SecType != "STK" {
		t.Fatalf("expected incomplete derivative-like symbol to downgrade to STK, got %q", inst.SecType)
	}
	if inst.Expiry != "" || inst.Right != "" || inst.Strike != 0 {
		t.Fatalf("expected derivative fields to be cleared, got %+v", inst)
	}
}

func TestBuildResearchPromptIncludesInstitutionalContext(t *testing.T) {
	client := &researchStubClient{}
	desk := NewDesk(llm.NewRouter(client, client, client), 0.65)

	sig := testSignal()
	sig.InstitutionalContext = "Institutional context:\n  colleague.from_desk=desk-geo-a\n  colleague.peer_trust=0.74"
	opp := testOpportunity()
	opp.EvidenceMeta = &evidence.Metadata{
		SourceTrust:          0.88,
		LeadTimeAverageHours: 2.3,
		LeadTimeObservations: 4,
		LeadTimeScore:        0.42,
		EvidenceScore:        0.81,
		FreshnessStatus:      "fresh",
		FreshnessAgeHours:    1.2,
		FreshnessWindowHours: 24,
		DistinctSources:      2,
		DistinctOwnerGroups:  2,
		DistinctLanguages:    2,
		HasPrimarySource:     true,
		ConfidenceVector:     &evidence.ConfidenceVector{FactConfidence: 0.82},
	}

	prompt := desk.buildResearchPrompt(opp, sig, nil, false)
	for _, want := range []string{
		"Institutional context:",
		"colleague.from_desk=desk-geo-a",
		"Historical lead time: avg 2.30h across 4 narratives (score 0.42)",
		"Source trust: 0.88",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected research prompt to include %q, got %q", want, prompt)
		}
	}
}

func testOpportunity() *model.Opportunity {
	return &model.Opportunity{
		ID: "opp-1",
		Instruments: []model.Instrument{{
			Symbol:   "TLT",
			SecType:  "STK",
			Currency: "USD",
			Exchange: "SMART",
		}},
		Direction: model.Long,
		Urgency:   0.8,
		Score:     78,
		Category:  "macro",
		SignalIDs: []string{"sig-1"},
		CreatedAt: time.Now(),
	}
}

func testSignal() signal.Signal {
	return signal.Signal{
		ID:         "sig-1",
		Source:     "fed-speeches",
		Type:       signal.TypeNews,
		Category:   "macro",
		Timestamp:  time.Now(),
		Urgency:    0.8,
		Translated: "Federal Reserve speech signaled a more hawkish balance of risks.",
	}
}

func validResearchJSON() string {
	return fmt.Sprintf(`{
  "structure": "single",
  "instrument": {"symbol": "TLT", "sec_type": "STK", "currency": "USD", "exchange": "SMART", "expiry": "", "strike": 0, "right": ""},
  "legs": [],
  "direction": "long",
  "entry_price": 90.5,
  "target_price": 96.0,
  "stop_loss": 88.0,
  "conviction": 0.74,
  "time_horizon_hours": 48,
  "position_size_pct": 0.01,
  "strategy": "macro",
  "surprise_assessment": {
    "truth_score": 0.8,
    "novelty_score": 0.7,
    "priced_in_score": 0.3,
    "reaction_gap_score": 0.6,
    "unmoved_asset_score": 0.5,
    "summary": "rates repricing is incomplete"
  },
  "evidence": ["hawkish policy rhetoric", "duration-sensitive setup", "clean rate proxy"],
  "counter_args": ["speech may be ignored", "positioning could already be crowded"],
  "kill_rules": [{"condition": "price_below_stop", "threshold": 88.0, "action": "close"}],
  "reasoning": "hawkish repricing favors duration rebound after overshoot"
}`)
}

func containsTerminalContract(prompt string) bool {
	return strings.Contains(prompt, terminalJSONStart) && strings.Contains(prompt, terminalJSONEnd)
}
