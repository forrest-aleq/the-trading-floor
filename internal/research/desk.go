package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/internal/institutional"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/marketcontext"
	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type flexibleFloat64 float64

func (f *flexibleFloat64) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*f = 0
		return nil
	}
	if strings.HasPrefix(raw, "\"") {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			*f = 0
			return nil
		}
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return err
		}
		*f = flexibleFloat64(parsed)
		return nil
	}
	var parsed float64
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*f = flexibleFloat64(parsed)
	return nil
}

func (f flexibleFloat64) Float64() float64 {
	return float64(f)
}

type flexibleInt int

func (i *flexibleInt) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*i = 0
		return nil
	}
	if strings.HasPrefix(raw, "\"") {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			*i = 0
			return nil
		}
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return err
		}
		*i = flexibleInt(int(parsed))
		return nil
	}
	var parsedInt int
	if err := json.Unmarshal(data, &parsedInt); err == nil {
		*i = flexibleInt(parsedInt)
		return nil
	}
	var parsedFloat float64
	if err := json.Unmarshal(data, &parsedFloat); err != nil {
		return err
	}
	*i = flexibleInt(int(parsedFloat))
	return nil
}

func (i flexibleInt) Int() int {
	return int(i)
}

// Desk orchestrates thesis formation through the trio conversation
type Desk struct {
	log            *slog.Logger
	llm            *llm.Router
	minConviction  float64
	marketContext  *marketcontext.Service
	selectedModel  string
	responseMode   structuredResponseMode
	retryModel     string
	compilerModel  string
	systemPrompt   string
	fastPrompt     string
	compactPrefix  string
	thoughtPrefix  string
	compilerPrompt string
}

func NewDesk(llmRouter *llm.Router, minConviction float64) *Desk {
	if minConviction == 0 {
		minConviction = 0.65
	}
	policy := activePromptPolicy()
	responseMode := strings.TrimSpace(os.Getenv("RESEARCH_RESPONSE_MODE"))
	if responseMode == "" {
		responseMode = "json"
	}
	selectedModel := researchSelectedModel()
	return &Desk{
		log:            slog.Default().With("component", "research"),
		llm:            llmRouter,
		minConviction:  minConviction,
		selectedModel:  selectedModel,
		responseMode:   detectStructuredResponseMode(responseMode, selectedModel),
		retryModel:     structuredRetryModel("RESEARCH_RETRY_MODEL", structuredCompilerModel("RESEARCH_COMPILER_MODEL"), selectedModel),
		compilerModel:  structuredCompilerModel("RESEARCH_COMPILER_MODEL"),
		systemPrompt:   policy.researchPrompt,
		fastPrompt:     policy.researchFastPrompt,
		compactPrefix:  policy.researchUserCompactPrefix,
		thoughtPrefix:  policy.researchThoughtPrefix,
		compilerPrompt: policy.researchCompilerPrompt,
	}
}

func (d *Desk) SetMarketContextService(service *marketcontext.Service) {
	d.marketContext = service
}

var (
	researchMaxTokens         = readStructuredIntEnv("RESEARCH_MAX_TOKENS", 1024)
	researchRequestTimeout    = readStructuredDurationEnv("RESEARCH_REQUEST_TIMEOUT", 18*time.Second)
	researchThoughtTimeout    = readStructuredDurationEnv("RESEARCH_THOUGHT_TIMEOUT", 30*time.Second)
	researchRetryTimeout      = readStructuredDurationEnv("RESEARCH_RETRY_TIMEOUT", 20*time.Second)
	researchRetryMaxTokens    = readStructuredIntEnv("RESEARCH_RETRY_MAX_TOKENS", 384)
	researchCompilerTimeout   = readStructuredDurationEnv("RESEARCH_COMPILER_TIMEOUT", 35*time.Second)
	researchCompilerMaxTokens = readStructuredIntEnv("RESEARCH_COMPILER_MAX_TOKENS", 900)
	researchPricingTimeout    = readStructuredDurationEnv("RESEARCH_PRICING_TIMEOUT", 1500*time.Millisecond)
	researchDefaultPosition   = readStructuredFloatEnv("RESEARCH_DEFAULT_POSITION_SIZE_PCT", 0.01)
)

type Investigation struct {
	Thesis     *model.Thesis
	Accepted   bool
	Reason     string
	Conviction float64
}

// Investigate takes an opportunity and produces a thesis
func (d *Desk) Investigate(ctx context.Context, opp *model.Opportunity, sig signal.Signal, deskID string) (*model.Thesis, error) {
	result, err := d.InvestigateDetailed(ctx, opp, sig, deskID)
	if err != nil {
		return nil, err
	}
	return result.Thesis, nil
}

func (d *Desk) InvestigateDetailed(ctx context.Context, opp *model.Opportunity, sig signal.Signal, deskID string) (Investigation, error) {
	var marketCtx *model.MarketContext
	if d.marketContext != nil {
		marketCtx = d.marketContext.BuildOpportunityContext(opp, sig)
	}
	if investigation, ok := d.deterministicKalshiInvestigation(ctx, opp, sig, deskID, marketCtx); ok {
		return investigation, nil
	}
	prompt := d.buildResearchPrompt(opp, sig, marketCtx, false)
	compactPrompt := d.buildResearchPrompt(opp, sig, marketCtx, true)

	resp, err := d.askResearchWithFallbackMode(ctx, prompt, compactPrompt)
	if err != nil {
		if recovered, ok := recoverGroundedResearchJSON("", opp, sig, marketCtx, scannerGroundedResearchRecoveryEnabled()); ok {
			d.log.Info("research recovered grounded thesis after LLM error",
				"desk", deskID,
				"symbol", firstOpportunitySymbol(opp),
				"error", err,
			)
			resp = recovered
		} else {
			return Investigation{Reason: "llm_error"}, fmt.Errorf("research LLM error: %w", err)
		}
	}

	cleaned, err := extractStructuredJSON(resp)
	if err != nil {
		if d.compilerModel != "" {
			if compiled, compileErr := d.compileResearchJSON(ctx, compactPrompt, resp); compileErr == nil {
				if compiledJSON, extractErr := extractStructuredJSON(compiled); extractErr == nil {
					cleaned = compiledJSON
					err = nil
					d.log.Info("research compiler recovered structured thesis",
						"desk", deskID,
						"compiler_model", d.compilerModel,
					)
				} else {
					err = extractErr
				}
			} else {
				d.log.Warn("research compiler fallback failed",
					"desk", deskID,
					"compiler_model", d.compilerModel,
					"error", compileErr,
				)
			}
		}
	}
	if err != nil {
		if recovered, ok := recoverGroundedResearchJSON(resp, opp, sig, marketCtx, scannerGroundedResearchRecoveryEnabled()); ok {
			cleaned = recovered
			err = nil
			d.log.Info("research recovered grounded thesis from thought trace",
				"desk", deskID,
				"symbol", firstOpportunitySymbol(opp),
			)
		}
	}
	if err != nil {
		d.log.Warn("research JSON extraction failed",
			"error", err,
			"response_len", len(resp),
			"response_excerpt", truncateForLog(resp, 320),
		)
		return Investigation{Reason: "json_extraction"}, fmt.Errorf("research JSON extraction: %w", err)
	}
	cleaned = enrichResearchJSON(cleaned, opp, marketCtx)

	if err := llm.ValidateJSONFields(cleaned, []string{"instrument", "direction"}); err != nil {
		return Investigation{Reason: "validation"}, fmt.Errorf("research response validation: %w", err)
	}

	var result researchResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return Investigation{Reason: "parse_error"}, fmt.Errorf("research parse error: %w", err)
	}

	// Build thesis
	evidence := make([]model.Evidence, len(result.Evidence))
	for i, e := range result.Evidence {
		signalID := ""
		if i < len(opp.SignalIDs) {
			signalID = opp.SignalIDs[i]
		}
		evidence[i] = model.Evidence{
			Source:   "signal",
			Content:  e,
			Weight:   1.0,
			SignalID: signalID,
		}
	}

	killRules := parseKillRules(result.KillRules, result.StopLoss.Float64())
	legs := normalizeTradeLegs(result.Legs, result.EntryPrice.Float64())
	structure := normalizeStructure(result.Structure, legs)
	primary := model.Instrument{
		Symbol:   result.Instrument.Symbol,
		SecType:  result.Instrument.SecType,
		Currency: result.Instrument.Currency,
		Exchange: normalizeExchange(result.Instrument.Exchange),
		Expiry:   result.Instrument.Expiry,
		Strike:   result.Instrument.Strike.Float64(),
		Right:    result.Instrument.Right,
	}
	primary = normalizeResearchInstrument(primary)
	if primary.Symbol == "" && len(legs) > 0 {
		primary = legs[0].Instrument
	}
	if primary.Symbol == "" {
		return Investigation{Reason: "no_instrument"}, fmt.Errorf("research validation: missing trade instrument")
	}

	thesis := &model.Thesis{
		ID:                 uuid.New().String(),
		OpportunityID:      opp.ID,
		DeskID:             deskID,
		Strategy:           normalizeStrategy(result.Strategy),
		Structure:          structure,
		Instrument:         primary,
		Legs:               legs,
		Direction:          model.TradeDirection(result.Direction),
		Conviction:         normalizeResearchConviction(result.Conviction.Float64(), opp),
		Health:             0.85, // Initial health
		Evidence:           evidence,
		CounterArgs:        result.CounterArgs,
		EntryPrice:         result.EntryPrice.Float64(),
		TargetPrice:        result.TargetPrice.Float64(),
		StopLoss:           result.StopLoss.Float64(),
		PositionSize:       normalizePositionSizePct(result.PositionSizePct.Float64()),
		TimeHorizon:        time.Duration(result.TimeHorizonHours.Int()) * time.Hour,
		KillRules:          killRules,
		Status:             model.ThesisEmbryo,
		EvidenceMeta:       opp.EvidenceMeta.Clone(),
		MarketContext:      marketCtx,
		SurpriseAssessment: buildSurpriseAssessment(result),
		CreatedAt:          time.Now(),
	}

	d.HydrateThesisPricing(ctx, thesis)

	d.log.Info("thesis formed",
		"id", thesis.ID,
		"desk", deskID,
		"symbol", thesis.DisplaySymbol(),
		"structure", thesis.Structure,
		"direction", thesis.Direction,
		"conviction", thesis.Conviction,
		"strategy", thesis.Strategy,
	)

	investigation := Investigation{
		Thesis:     thesis,
		Accepted:   thesis.Conviction >= d.minConviction,
		Conviction: thesis.Conviction,
	}
	if !investigation.Accepted {
		investigation.Reason = "conviction_below_threshold"
	}
	return investigation, nil
}

func (d *Desk) deterministicKalshiInvestigation(ctx context.Context, opp *model.Opportunity, sig signal.Signal, deskID string, marketCtx *model.MarketContext) (Investigation, bool) {
	fastPathMode := kalshiDeterministicFastPathMode()
	if fastPathMode == "" || !strings.EqualFold(strings.TrimSpace(sig.Source), "kalshi-market") {
		return Investigation{}, false
	}
	inst, ok := kalshiOpportunityInstrument(opp)
	if !ok {
		return Investigation{}, false
	}

	entryPrice := kalshiSignalEntryPrice(sig, opp.Direction)
	if entryPrice <= 0 {
		entryPrice = fallbackResearchEntryPrice(marketCtx)
	}

	conviction := normalizeResearchConviction(0, opp)
	for _, floor := range []float64{
		d.minConviction,
		readStructuredFloatEnv("KALSHI_MIN_CONVICTION", d.minConviction),
	} {
		if normalized := normalizeConviction(floor); conviction < normalized {
			conviction = normalized
		}
	}
	if conviction > 0.68 {
		conviction = 0.68
	}

	thesis := &model.Thesis{
		ID:            uuid.New().String(),
		OpportunityID: opp.ID,
		DeskID:        deskID,
		Strategy:      "event",
		Structure:     "single",
		Instrument:    inst,
		Direction:     opp.Direction,
		Conviction:    conviction,
		Health:        0.8,
		Evidence: []model.Evidence{{
			Source:   "signal",
			Content:  deterministicKalshiEvidence(sig),
			Weight:   1.0,
			SignalID: firstSignalID(opp),
		}},
		CounterArgs:   deterministicKalshiCounterArgs(fastPathMode),
		EntryPrice:    entryPrice,
		TargetPrice:   0,
		StopLoss:      0,
		PositionSize:  normalizePositionSizePct(0),
		TimeHorizon:   time.Duration(recoverTimeHorizonHours(opp)) * time.Hour,
		KillRules:     []model.KillRule{},
		Status:        model.ThesisEmbryo,
		EvidenceMeta:  opp.EvidenceMeta.Clone(),
		MarketContext: marketCtx,
		SurpriseAssessment: &model.SurpriseAssessment{
			Summary: deterministicKalshiSummary(fastPathMode),
		},
		CreatedAt: time.Now(),
	}
	d.HydrateThesisPricing(ctx, thesis)
	if thesis.EntryPrice <= 0 {
		thesis.EntryPrice = entryPrice
	}

	d.log.Info("deterministic Kalshi thesis formed",
		"id", thesis.ID,
		"desk", deskID,
		"symbol", thesis.DisplaySymbol(),
		"direction", thesis.Direction,
		"conviction", thesis.Conviction,
		"entry_price", thesis.EntryPrice,
		"fast_path_mode", fastPathMode,
	)

	investigation := Investigation{
		Thesis:     thesis,
		Accepted:   thesis.Conviction >= d.minConviction,
		Conviction: thesis.Conviction,
	}
	if !investigation.Accepted {
		investigation.Reason = "conviction_below_threshold"
	}
	return investigation, true
}

func (d *Desk) HydrateThesisPricing(ctx context.Context, thesis *model.Thesis) {
	if thesis == nil || d.marketContext == nil {
		return
	}

	pricingCtx, cancel := withStructuredBudgetFraction(ctx, researchPricingTimeout, 0.2)
	defer cancel()
	enriched := d.marketContext.BuildThesisContext(pricingCtx, thesis)
	if enriched == nil {
		return
	}
	thesis.MarketContext = enriched
	if !thesis.IsMultiLeg() {
		thesis.Instrument = enriched.Instrument
	}
	if thesis.EntryPrice <= 0 && enriched.CurrentPrice > 0 {
		thesis.EntryPrice = enriched.CurrentPrice
	}
}

func (d *Desk) askResearchWithFallbackMode(ctx context.Context, prompt, compactPrompt string) (string, error) {
	if d.responseMode == structuredResponseModeThought {
		thoughtPrompt := addTerminalJSONContract(d.thoughtPrefix + "\n\n" + d.systemPrompt)
		primaryCtx, cancel := withStructuredBudgetFraction(ctx, researchThoughtTimeout, 1.0)
		resp, err := d.askPrimaryResearch(primaryCtx, thoughtPrompt, prompt, false)
		cancel()
		if err != nil {
			if !hasStructuredBudget(ctx, minStructuredAttemptBudget) {
				return "", err
			}
			retryCtx, retryCancel := withStructuredBudgetFraction(ctx, researchRetryTimeout, 0.5)
			retryResp, retryErr := d.retryStructuredJSON(retryCtx, compactPrompt)
			retryCancel()
			if retryErr == nil {
				if _, retryParseErr := extractStructuredJSON(retryResp); retryParseErr == nil {
					d.log.Info("research structured retry recovered primary LLM failure",
						"model", d.retryModel,
						"error", err,
					)
					return retryResp, nil
				}
			}
			return "", err
		}
		if _, err := extractStructuredJSON(resp); err == nil {
			return resp, nil
		}

		if !hasStructuredBudget(ctx, minStructuredAttemptBudget) {
			return resp, nil
		}
		retryCtx, retryCancel := withStructuredBudgetFraction(ctx, researchRetryTimeout, 0.5)
		retryResp, retryErr := d.retryStructuredJSON(retryCtx, compactPrompt)
		retryCancel()
		if retryErr == nil {
			if _, err := extractStructuredJSON(retryResp); err != nil {
				return resp, nil
			}
			d.log.Info("research structured retry recovered terminal JSON miss",
				"model", d.selectedModel,
			)
			return retryResp, nil
		}
		return resp, nil
	}

	primaryCtx, cancel := withStructuredBudgetFraction(ctx, researchRequestTimeout, 1.0)
	resp, err := d.askPrimaryResearch(primaryCtx, d.systemPrompt, prompt, true)
	cancel()
	if err != nil {
		if !hasStructuredBudget(ctx, minStructuredAttemptBudget) {
			return "", err
		}
		retryCtx, retryCancel := withStructuredBudgetFraction(ctx, researchRetryTimeout, 0.5)
		retryResp, retryErr := d.retryStructuredJSON(retryCtx, compactPrompt)
		retryCancel()
		if retryErr == nil {
			if _, retryParseErr := extractStructuredJSON(retryResp); retryParseErr == nil {
				d.log.Info("research structured retry recovered primary JSON failure",
					"model", d.retryModel,
					"error", err,
				)
				return retryResp, nil
			}
		}
		return "", err
	}
	if _, err := extractStructuredJSON(resp); err == nil {
		return resp, nil
	}
	if !hasStructuredBudget(ctx, minStructuredAttemptBudget) {
		return resp, nil
	}
	retryCtx, retryCancel := withStructuredBudgetFraction(ctx, researchRetryTimeout, 0.5)
	retryResp, retryErr := d.retryStructuredJSON(retryCtx, compactPrompt)
	retryCancel()
	if retryErr == nil {
		if _, err := extractStructuredJSON(retryResp); err != nil {
			return resp, nil
		}
		d.log.Info("research structured retry recovered JSON mode terminal miss",
			"model", d.selectedModel,
		)
		return retryResp, nil
	}
	return resp, nil
}

func (d *Desk) askPrimaryResearch(ctx context.Context, system, prompt string, jsonMode bool) (string, error) {
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: prompt},
		},
		Model:       d.selectedModel,
		Tier:        llm.TierAnalysis,
		MaxTokens:   researchMaxTokens,
		Temperature: 0.2,
		JSONMode:    jsonMode,
	}
	resp, err := d.llm.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (d *Desk) retryStructuredJSON(ctx context.Context, prompt string) (string, error) {
	retryTier := llm.TierAnalysis
	if strings.TrimSpace(d.retryModel) != "" && strings.TrimSpace(d.retryModel) != strings.TrimSpace(d.selectedModel) {
		retryTier = llm.TierSpeed
	}
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: d.fastPrompt},
			{Role: llm.RoleUser, Content: prompt},
		},
		Model:       d.retryModel,
		Tier:        retryTier,
		MaxTokens:   minResearchRetryTokens(researchMaxTokens),
		Temperature: 0.1,
		JSONMode:    true,
	}
	resp, err := d.llm.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (d *Desk) compileResearchJSON(ctx context.Context, originalPrompt, rawResponse string) (string, error) {
	compileCtx, cancel := withStructuredBudgetFraction(ctx, researchCompilerTimeout, 1.0)
	defer cancel()

	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: d.compilerPrompt},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Original research task:\n%s\n\nResearch reasoning transcript:\n%s", originalPrompt, truncateForCompiler(rawResponse, 1200))},
		},
		Model:       d.compilerModel,
		Tier:        llm.TierSpeed,
		MaxTokens:   researchCompilerMaxTokens,
		Temperature: 0.0,
		JSONMode:    true,
	}

	resp, err := d.llm.Complete(compileCtx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

type researchResult struct {
	Structure  string `json:"structure"`
	Instrument struct {
		Symbol   string          `json:"symbol"`
		SecType  string          `json:"sec_type"`
		Currency string          `json:"currency"`
		Exchange string          `json:"exchange"`
		Expiry   string          `json:"expiry"`
		Strike   flexibleFloat64 `json:"strike"`
		Right    string          `json:"right"`
	} `json:"instrument"`
	Legs []struct {
		Instrument struct {
			Symbol   string          `json:"symbol"`
			SecType  string          `json:"sec_type"`
			Currency string          `json:"currency"`
			Exchange string          `json:"exchange"`
			Expiry   string          `json:"expiry"`
			Strike   flexibleFloat64 `json:"strike"`
			Right    string          `json:"right"`
		} `json:"instrument"`
		Direction  string          `json:"direction"`
		Ratio      flexibleFloat64 `json:"ratio"`
		EntryPrice flexibleFloat64 `json:"entry_price"`
	} `json:"legs"`
	Direction          string          `json:"direction"`
	EntryPrice         flexibleFloat64 `json:"entry_price"`
	TargetPrice        flexibleFloat64 `json:"target_price"`
	StopLoss           flexibleFloat64 `json:"stop_loss"`
	Conviction         flexibleFloat64 `json:"conviction"`
	TimeHorizonHours   flexibleInt     `json:"time_horizon_hours"`
	PositionSizePct    flexibleFloat64 `json:"position_size_pct"`
	Strategy           string          `json:"strategy"`
	SurpriseAssessment struct {
		TruthScore        flexibleFloat64 `json:"truth_score"`
		NoveltyScore      flexibleFloat64 `json:"novelty_score"`
		PricedInScore     flexibleFloat64 `json:"priced_in_score"`
		ReactionGapScore  flexibleFloat64 `json:"reaction_gap_score"`
		UnmovedAssetScore flexibleFloat64 `json:"unmoved_asset_score"`
		Summary           string          `json:"summary"`
	} `json:"surprise_assessment"`
	Evidence    []string        `json:"evidence"`
	CounterArgs []string        `json:"counter_args"`
	KillRules   json.RawMessage `json:"kill_rules"`
	Reasoning   string          `json:"reasoning"`
}

func instrumentNames(instruments []model.Instrument) []string {
	names := make([]string, len(instruments))
	for i, inst := range instruments {
		names[i] = inst.Label()
	}
	return names
}

func normalizeTradeLegs(raw []struct {
	Instrument struct {
		Symbol   string          `json:"symbol"`
		SecType  string          `json:"sec_type"`
		Currency string          `json:"currency"`
		Exchange string          `json:"exchange"`
		Expiry   string          `json:"expiry"`
		Strike   flexibleFloat64 `json:"strike"`
		Right    string          `json:"right"`
	} `json:"instrument"`
	Direction  string          `json:"direction"`
	Ratio      flexibleFloat64 `json:"ratio"`
	EntryPrice flexibleFloat64 `json:"entry_price"`
}, fallbackEntry float64) []model.TradeLeg {
	if len(raw) == 0 {
		return nil
	}

	legs := make([]model.TradeLeg, 0, len(raw))
	for _, leg := range raw {
		if strings.TrimSpace(leg.Instrument.Symbol) == "" {
			continue
		}
		direction := model.TradeDirection(strings.ToLower(strings.TrimSpace(leg.Direction)))
		if direction != model.Short {
			direction = model.Long
		}
		ratio := leg.Ratio.Float64()
		if ratio <= 0 {
			ratio = 1
		}
		entryPrice := leg.EntryPrice.Float64()
		if entryPrice <= 0 {
			entryPrice = fallbackEntry
		}
		inst := normalizeResearchInstrument(model.Instrument{
			Symbol:   leg.Instrument.Symbol,
			SecType:  leg.Instrument.SecType,
			Currency: leg.Instrument.Currency,
			Exchange: normalizeExchange(leg.Instrument.Exchange),
			Expiry:   leg.Instrument.Expiry,
			Strike:   leg.Instrument.Strike.Float64(),
			Right:    leg.Instrument.Right,
		})
		legs = append(legs, model.TradeLeg{
			Instrument: inst,
			Direction:  direction,
			Ratio:      ratio,
			EntryPrice: entryPrice,
		})
	}

	if len(legs) <= 1 {
		return nil
	}
	return legs
}

func parseKillRules(raw json.RawMessage, stopLoss float64) []model.KillRule {
	if len(raw) == 0 {
		return defaultKillRules(stopLoss)
	}

	var structured []struct {
		Condition string  `json:"condition"`
		Threshold float64 `json:"threshold"`
		Action    string  `json:"action"`
	}
	if err := json.Unmarshal(raw, &structured); err == nil {
		killRules := make([]model.KillRule, 0, len(structured))
		for _, kr := range structured {
			if strings.TrimSpace(kr.Condition) == "" {
				continue
			}
			action := strings.TrimSpace(kr.Action)
			if action == "" {
				action = "alert"
			}
			killRules = append(killRules, model.KillRule{
				Condition: kr.Condition,
				Threshold: kr.Threshold,
				Action:    action,
			})
		}
		if len(killRules) > 0 {
			return killRules
		}
	}

	var stringRules []string
	if err := json.Unmarshal(raw, &stringRules); err == nil {
		killRules := make([]model.KillRule, 0, len(stringRules))
		for _, rule := range stringRules {
			rule = strings.TrimSpace(rule)
			if rule == "" {
				continue
			}
			killRules = append(killRules, model.KillRule{
				Condition: rule,
				Threshold: stopLoss,
				Action:    "alert",
			})
		}
		if len(killRules) > 0 {
			return killRules
		}
	}

	return defaultKillRules(stopLoss)
}

func defaultKillRules(stopLoss float64) []model.KillRule {
	if stopLoss <= 0 {
		return nil
	}
	return []model.KillRule{{
		Condition: "price_below_stop",
		Threshold: stopLoss,
		Action:    "close",
	}}
}

func normalizeConviction(value float64) float64 {
	switch {
	case value > 1 && value <= 100:
		value /= 100
	case value > 100:
		value = 1
	case value < 0:
		value = 0
	}
	if value > 1 {
		value = 1
	}
	return value
}

func normalizeResearchInstrument(inst model.Instrument) model.Instrument {
	inst.Symbol = strings.TrimSpace(inst.Symbol)
	inst.Exchange = normalizeExchange(inst.Exchange)
	if strings.TrimSpace(inst.Currency) == "" {
		inst.Currency = "USD"
	}
	if inst.IsKalshi() {
		return model.NormalizeKalshiInstrument(inst)
	}
	if parsed, ok := parseOptionContractString(inst.Symbol); ok {
		if strings.TrimSpace(inst.Symbol) == "" || strings.EqualFold(inst.SecType, "STK") || strings.TrimSpace(inst.SecType) == "" {
			inst.Symbol = parsed.Symbol
			inst.SecType = parsed.SecType
			inst.Multiplier = parsed.Multiplier
		}
		if inst.Expiry == "" {
			inst.Expiry = parsed.Expiry
		}
		if inst.Strike <= 0 {
			inst.Strike = parsed.Strike
		}
		if strings.TrimSpace(inst.Right) == "" {
			inst.Right = parsed.Right
		}
		if strings.TrimSpace(inst.Symbol) == "" {
			inst.Symbol = parsed.Symbol
		}
	}
	if strings.TrimSpace(inst.SecType) == "" {
		inst.SecType = "STK"
	}
	if (inst.SecType == "OPT" || inst.SecType == "FOP") && strings.TrimSpace(inst.Multiplier) == "" {
		inst.Multiplier = "100"
	}
	inst.Right = normalizeOptionRight(inst.Right)
	inst.Expiry = normalizeContractExpiry(inst.Expiry)
	if shouldDowngradeMalformedDerivativeLikeInstrument(inst) {
		inst = downgradeDerivativeToUnderlying(inst)
	}
	if shouldDowngradeStaleDerivative(inst) {
		inst = downgradeDerivativeToUnderlying(inst)
	}
	return inst
}

func shouldDowngradeMalformedDerivativeLikeInstrument(inst model.Instrument) bool {
	secType := strings.ToUpper(strings.TrimSpace(inst.SecType))
	if secType == "OPT" || secType == "FOP" {
		return inst.Expiry == "" || inst.Strike <= 0 || strings.TrimSpace(inst.Right) == ""
	}

	symbol := strings.TrimSpace(inst.Symbol)
	if symbol == "" || !strings.Contains(symbol, " ") {
		return false
	}
	if inst.Expiry != "" || inst.Strike > 0 || strings.TrimSpace(inst.Right) != "" {
		return false
	}

	underlying := normalizeContractUnderlying(symbol)
	if underlying == "" || strings.EqualFold(strings.TrimSpace(underlying), symbol) {
		return false
	}

	parts := strings.Fields(symbol)
	for _, token := range parts[1:] {
		if looksDerivativeLikeToken(token) {
			return true
		}
	}
	return false
}

func looksDerivativeLikeToken(token string) bool {
	token = strings.ToUpper(strings.TrimSpace(token))
	if token == "" {
		return false
	}
	if normalizeContractExpiry(token) != "" {
		return true
	}
	if parseDerivativeStrikeToken(token) {
		return true
	}
	switch token {
	case "CALL", "PUT", "C", "P":
		return true
	default:
		return false
	}
}

func parseDerivativeStrikeToken(token string) bool {
	for _, suffix := range []string{"CALL", "PUT", "C", "P"} {
		if !strings.HasSuffix(token, suffix) {
			continue
		}
		strikeText := strings.TrimSpace(token[:len(token)-len(suffix)])
		if strikeText == "" {
			return false
		}
		strike, err := strconv.ParseFloat(strikeText, 64)
		return err == nil && strike > 0
	}
	return false
}

func parseOptionContractString(raw string) (model.Instrument, bool) {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) < 3 {
		return model.Instrument{}, false
	}

	expiry := normalizeContractExpiry(parts[1])
	if expiry == "" {
		return model.Instrument{}, false
	}

	strikeToken := parts[2]
	rightToken := ""
	if len(parts) >= 4 {
		rightToken = parts[3]
	}

	strike, right, ok := parseOptionStrikeRight(strikeToken, rightToken)
	if !ok {
		return model.Instrument{}, false
	}

	return model.Instrument{
		Symbol:     normalizeContractUnderlying(parts[0]),
		SecType:    "OPT",
		Currency:   "USD",
		Exchange:   "SMART",
		Expiry:     expiry,
		Strike:     strike,
		Right:      right,
		Multiplier: "100",
	}, true
}

func shouldDowngradeStaleDerivative(inst model.Instrument) bool {
	policy := strings.TrimSpace(strings.ToLower(os.Getenv("RESEARCH_STALE_DERIVATIVE_POLICY")))
	if policy == "allow" {
		return false
	}
	secType := strings.TrimSpace(strings.ToUpper(inst.SecType))
	if secType != "OPT" && secType != "FOP" {
		return false
	}
	expiry := normalizeContractExpiry(inst.Expiry)
	if expiry == "" {
		return false
	}
	parsed, err := time.Parse("20060102", expiry)
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	return parsed.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC))
}

func downgradeDerivativeToUnderlying(inst model.Instrument) model.Instrument {
	symbol := normalizeContractUnderlying(inst.Symbol)
	if symbol == "" {
		symbol = strings.TrimSpace(inst.Symbol)
	}
	currency := strings.TrimSpace(inst.Currency)
	if currency == "" {
		currency = "USD"
	}
	return model.Instrument{
		ConID:    0,
		Symbol:   symbol,
		SecType:  "STK",
		Exchange: normalizeExchange(inst.Exchange),
		Currency: currency,
	}
}

func normalizeContractUnderlying(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	token := strings.Fields(value)[0]
	if token == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range token {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r == '.' || r == '-':
			b.WriteRune(r)
		default:
			if b.Len() > 0 {
				return b.String()
			}
			return token
		}
	}
	if b.Len() == 0 {
		return token
	}
	return b.String()
}

func parseOptionStrikeRight(strikeToken, rightToken string) (float64, string, bool) {
	strikeToken = strings.TrimSpace(strikeToken)
	rightToken = strings.TrimSpace(rightToken)
	combinedUpper := strings.ToUpper(strikeToken)
	for _, suffix := range []string{"CALL", "PUT", "C", "P"} {
		if strings.HasSuffix(combinedUpper, suffix) {
			strikeText := strings.TrimSpace(strikeToken[:len(strikeToken)-len(suffix)])
			strike, err := strconv.ParseFloat(strikeText, 64)
			if err != nil || strike <= 0 {
				return 0, "", false
			}
			return strike, normalizeOptionRight(suffix), true
		}
	}

	strike, err := strconv.ParseFloat(strikeToken, 64)
	if err != nil || strike <= 0 {
		return 0, "", false
	}
	right := normalizeOptionRight(rightToken)
	if right == "" {
		return 0, "", false
	}
	return strike, right, true
}

func normalizeOptionRight(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "CALL", "C":
		return "C"
	case "PUT", "P":
		return "P"
	default:
		return ""
	}
}

func normalizeContractExpiry(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) == len("2006-01-02") {
		if parsed, err := time.Parse("2006-01-02", value); err == nil {
			return parsed.Format("20060102")
		}
	}
	if len(value) == len("20060102") {
		if _, err := time.Parse("20060102", value); err == nil {
			return value
		}
	}
	return ""
}

func normalizeResearchConviction(value float64, opp *model.Opportunity) float64 {
	value = normalizeConviction(value)
	if value > 0 {
		return value
	}
	if opp == nil {
		return 0
	}
	return normalizeConviction(opp.Score / 100)
}

func enrichResearchJSON(cleaned string, opp *model.Opportunity, marketCtx *model.MarketContext) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return cleaned
	}

	if inst, ok := kalshiOpportunityInstrument(opp); ok {
		payload["structure"] = "single"
		payload["instrument"] = instrumentPayload(inst)
		payload["legs"] = []any{}
		if researchDirectionMissing(payload["direction"]) && opp.Direction != "" {
			payload["direction"] = string(opp.Direction)
		}
		normalizeKalshiPayloadPrice(payload, "entry_price", marketCtx)
		normalizeKalshiPayloadPrice(payload, "target_price", nil)
		normalizeKalshiPayloadPrice(payload, "stop_loss", nil)
		encoded, err := json.Marshal(payload)
		if err != nil {
			return cleaned
		}
		return string(encoded)
	}

	if !hasInstrumentPayload(payload["instrument"]) && opp != nil && len(opp.Instruments) > 0 {
		payload["instrument"] = instrumentPayload(opp.Instruments[0])
	}
	if researchDirectionMissing(payload["direction"]) && opp != nil && opp.Direction != "" {
		payload["direction"] = string(opp.Direction)
	}
	if value, ok := numericValue(payload["entry_price"]); !ok || value <= 0 {
		if price := fallbackResearchEntryPrice(marketCtx); price > 0 {
			payload["entry_price"] = price
		}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return cleaned
	}
	return string(encoded)
}

func kalshiOpportunityInstrument(opp *model.Opportunity) (model.Instrument, bool) {
	if opp == nil {
		return model.Instrument{}, false
	}
	for _, inst := range opp.Instruments {
		if inst.IsKalshi() {
			return model.NormalizeKalshiInstrument(inst), true
		}
	}
	return model.Instrument{}, false
}

type researchKalshiSnapshot struct {
	Title            string `json:"title"`
	Subtitle         string `json:"subtitle"`
	YesBidDollars    string `json:"yes_bid_dollars"`
	YesAskDollars    string `json:"yes_ask_dollars"`
	NoBidDollars     string `json:"no_bid_dollars"`
	NoAskDollars     string `json:"no_ask_dollars"`
	LastPriceDollars string `json:"last_price_dollars"`
}

func paperDiscoveryKalshiFastPathEnabled() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("RESEARCH_KALSHI_DETERMINISTIC")), "false") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("FLOOR_RUNTIME_MODE")), "paper_discovery") &&
		!strings.EqualFold(strings.TrimSpace(os.Getenv("KALSHI_LIVE_TRADING")), "true")
}

func kalshiDeterministicFastPathMode() string {
	if paperDiscoveryKalshiFastPathEnabled() {
		return "paper_discovery"
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("RESEARCH_KALSHI_DETERMINISTIC")), "false") {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("KALSHI_LIVE_TRADING")), "true") {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("KALSHI_LIVE_DETERMINISTIC_FAST_PATH")), "true") {
		return ""
	}
	return "live"
}

func deterministicKalshiCounterArgs(mode string) []string {
	switch mode {
	case "live":
		return []string{
			"Live deterministic Kalshi fast path: bypasses model-backed research while OpenRouter capacity is constrained.",
			"Order remains subject to Kalshi live safety caps, duplicate cooldown, and execution validation.",
		}
	default:
		return []string{
			"Paper-discovery deterministic research path; requires later model-backed validation before live capital.",
		}
	}
}

func deterministicKalshiSummary(mode string) string {
	if mode == "live" {
		return "Live Kalshi deterministic fast path using scanner-selected ticker, side, and live market quote."
	}
	return "Paper-discovery Kalshi fast path using scanner-selected ticker, side, and live market quote."
}

func kalshiSignalEntryPrice(sig signal.Signal, direction model.TradeDirection) float64 {
	var snap researchKalshiSnapshot
	if len(sig.Raw) == 0 || json.Unmarshal(sig.Raw, &snap) != nil {
		return 0
	}
	switch direction {
	case model.Short:
		return firstKalshiProbability(snap.NoAskDollars, snap.NoBidDollars)
	default:
		return firstKalshiProbability(snap.YesAskDollars, snap.LastPriceDollars, snap.YesBidDollars)
	}
}

func firstKalshiProbability(values ...string) float64 {
	for _, value := range values {
		parsed, ok := parseKalshiProbability(value)
		if ok {
			return parsed
		}
	}
	return 0
}

func parseKalshiProbability(raw string) (float64, bool) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "$"))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return normalizeKalshiProbability(value)
}

func deterministicKalshiEvidence(sig signal.Signal) string {
	var snap researchKalshiSnapshot
	if len(sig.Raw) > 0 && json.Unmarshal(sig.Raw, &snap) == nil {
		if title := strings.TrimSpace(firstNonEmptyString(snap.Title, snap.Subtitle)); title != "" {
			return institutional.TruncateForPrompt(title, 220)
		}
	}
	if text := strings.TrimSpace(firstNonEmptyString(sig.Translated, sig.OriginalText)); text != "" {
		return institutional.TruncateForPrompt(text, 220)
	}
	return "Kalshi market discovery signal selected by deterministic scanner."
}

func firstSignalID(opp *model.Opportunity) string {
	if opp == nil || len(opp.SignalIDs) == 0 {
		return ""
	}
	return opp.SignalIDs[0]
}

func normalizeKalshiPayloadPrice(payload map[string]any, key string, marketCtx *model.MarketContext) {
	if payload == nil || strings.TrimSpace(key) == "" {
		return
	}
	if value, ok := numericValue(payload[key]); ok {
		if normalized, priceOK := normalizeKalshiProbability(value); priceOK {
			payload[key] = normalized
			return
		}
	}
	if key == "entry_price" && marketCtx != nil {
		for _, candidate := range []float64{
			marketCtx.MidPrice,
			marketCtx.CurrentPrice,
			marketCtx.AskPrice,
			marketCtx.BidPrice,
		} {
			if normalized, ok := normalizeKalshiProbability(candidate); ok {
				payload[key] = normalized
				return
			}
		}
	}
}

func normalizeKalshiProbability(value float64) (float64, bool) {
	switch {
	case value > 0 && value < 1:
		return value, true
	case value > 1 && value < 100:
		return value / 100.0, true
	default:
		return 0, false
	}
}

func hasInstrumentPayload(value any) bool {
	instrument, ok := value.(map[string]any)
	if !ok {
		return false
	}
	symbol, ok := instrument["symbol"]
	if !ok || symbol == nil {
		return false
	}
	return strings.TrimSpace(fmt.Sprint(symbol)) != ""
}

func researchDirectionMissing(value any) bool {
	if value == nil {
		return true
	}
	text := strings.TrimSpace(strings.ToLower(fmt.Sprint(value)))
	switch text {
	case "", "<nil>", "null", "none", "n/a":
		return true
	default:
		return false
	}
}

func instrumentPayload(inst model.Instrument) map[string]any {
	return map[string]any{
		"symbol":   inst.Symbol,
		"sec_type": inst.SecType,
		"currency": inst.Currency,
		"exchange": normalizeExchange(inst.Exchange),
		"expiry":   inst.Expiry,
		"strike":   inst.Strike,
		"right":    inst.Right,
	}
}

func fallbackResearchEntryPrice(marketCtx *model.MarketContext) float64 {
	if marketCtx == nil {
		return 0
	}
	if marketCtx.CurrentPrice > 0 {
		return marketCtx.CurrentPrice
	}
	return 0
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func normalizePositionSizePct(value float64) float64 {
	switch {
	case value <= 0:
		value = researchDefaultPosition
	case value > 1 && value <= 100:
		value /= 100
	case value > 100:
		value = 1
	}
	return value
}

func recoverGroundedResearchJSON(raw string, opp *model.Opportunity, sig signal.Signal, marketCtx *model.MarketContext, allowScannerOnly bool) (string, bool) {
	raw = strings.TrimSpace(raw)
	if opp == nil || len(opp.Instruments) == 0 {
		return "", false
	}
	if raw == "" && !allowScannerOnly {
		return "", false
	}
	if raw != "" && !allowScannerOnly && !looksLikeGroundableThoughtTrace(raw) {
		return "", false
	}

	inst := normalizeResearchInstrument(opp.Instruments[0])
	if inst.Symbol == "" {
		return "", false
	}

	entryPrice := 0.0
	if marketCtx != nil && marketCtx.CurrentPrice > 0 {
		entryPrice = marketCtx.CurrentPrice
	}

	conviction := normalizeResearchConviction(0, opp) - 0.10
	if conviction < 0.20 {
		conviction = 0.20
	}

	payload := map[string]any{
		"structure": "single",
		"instrument": map[string]any{
			"symbol":   inst.Symbol,
			"sec_type": inst.SecType,
			"currency": firstNonEmptyString(inst.Currency, "USD"),
			"exchange": firstNonEmptyString(inst.Exchange, "SMART"),
			"expiry":   inst.Expiry,
			"strike":   inst.Strike,
			"right":    inst.Right,
		},
		"legs":               []any{},
		"direction":          string(opp.Direction),
		"entry_price":        entryPrice,
		"target_price":       0.0,
		"stop_loss":          0.0,
		"conviction":         conviction,
		"time_horizon_hours": recoverTimeHorizonHours(opp),
		"position_size_pct":  normalizePositionSizePct(0),
		"strategy":           normalizeStrategy(""),
		"surprise_assessment": map[string]any{
			"truth_score":         0.0,
			"novelty_score":       0.0,
			"priced_in_score":     0.0,
			"reaction_gap_score":  0.0,
			"unmoved_asset_score": 0.0,
			"summary":             "Recovered from research thought trace using scanner-grounded opportunity context.",
		},
		"evidence":     recoverEvidence(sig, raw),
		"counter_args": []string{"Recovered from noncompliant research trace; requires prosecution and council scrutiny."},
		"kill_rules":   []any{},
		"reasoning":    "Recovered from noncompliant research trace using the scanner-selected instrument and direction.",
	}

	cleaned, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	return string(cleaned), true
}

func scannerGroundedResearchRecoveryEnabled() bool {
	if raw := strings.TrimSpace(os.Getenv("RESEARCH_GROUNDED_RECOVERY")); raw != "" {
		return parseBoolLoose(raw)
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("FLOOR_RUNTIME_MODE")))
	return mode == "paper" || mode == "discovery"
}

func parseBoolLoose(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func looksLikeGroundableThoughtTrace(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(normalized, "<think>") ||
		strings.Contains(normalized, "we are given an opportunity to trade") ||
		strings.Contains(normalized, "the signal is") ||
		strings.Contains(normalized, "the instruments mentioned are")
}

func recoverTimeHorizonHours(opp *model.Opportunity) int {
	if opp == nil {
		return 24
	}
	switch {
	case opp.Urgency >= 0.85:
		return 24
	case opp.Urgency >= 0.65:
		return 48
	default:
		return 72
	}
}

func recoverEvidence(sig signal.Signal, raw string) []string {
	evidenceItems := []string{}
	if summary := strings.TrimSpace(sig.Translated); summary != "" {
		evidenceItems = append(evidenceItems, institutional.TruncateForPrompt(summary, 180))
	}
	if summary := strings.TrimSpace(sig.OriginalText); summary != "" && summary != strings.TrimSpace(sig.Translated) {
		evidenceItems = append(evidenceItems, institutional.TruncateForPrompt(summary, 140))
	}
	if snippet := recoverReasoningSnippet(raw); snippet != "" {
		evidenceItems = append(evidenceItems, snippet)
	}
	if len(evidenceItems) == 0 {
		evidenceItems = append(evidenceItems, "Recovered from scanner-grounded opportunity context.")
	}
	if len(evidenceItems) > 3 {
		evidenceItems = evidenceItems[:3]
	}
	return evidenceItems
}

func recoverReasoningSnippet(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	normalized := strings.NewReplacer("<think>", "", "</think>", "").Replace(raw)
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}
	lines := strings.FieldsFunc(normalized, func(r rune) bool { return r == '\n' || r == '\r' })
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if line == "" {
			continue
		}
		return institutional.TruncateForPrompt(line, 180)
	}
	return institutional.TruncateForPrompt(normalized, 180)
}

func firstOpportunitySymbol(opp *model.Opportunity) string {
	if opp == nil || len(opp.Instruments) == 0 {
		return ""
	}
	return strings.TrimSpace(opp.Instruments[0].Symbol)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func clampUnit(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func buildSurpriseAssessment(result researchResult) *model.SurpriseAssessment {
	assessment := &model.SurpriseAssessment{
		TruthScore:        clampUnit(result.SurpriseAssessment.TruthScore.Float64()),
		NoveltyScore:      clampUnit(result.SurpriseAssessment.NoveltyScore.Float64()),
		PricedInScore:     clampUnit(result.SurpriseAssessment.PricedInScore.Float64()),
		ReactionGapScore:  clampUnit(result.SurpriseAssessment.ReactionGapScore.Float64()),
		UnmovedAssetScore: clampUnit(result.SurpriseAssessment.UnmovedAssetScore.Float64()),
		Summary:           strings.TrimSpace(result.SurpriseAssessment.Summary),
	}
	if assessment.TruthScore == 0 &&
		assessment.NoveltyScore == 0 &&
		assessment.PricedInScore == 0 &&
		assessment.ReactionGapScore == 0 &&
		assessment.UnmovedAssetScore == 0 &&
		assessment.Summary == "" {
		return nil
	}
	return assessment
}

func normalizeStrategy(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "":
		return "event"
	case strings.Contains(value, "tail"):
		return "tail"
	case strings.Contains(value, "macro"):
		return "macro"
	case strings.Contains(value, "contrarian"):
		return "contrarian"
	case strings.Contains(value, "fundamental"):
		return "fundamental"
	case strings.Contains(value, "scalp"):
		return "scalper"
	case strings.Contains(value, "event"), strings.Contains(value, "earnings"), strings.Contains(value, "catalyst"), strings.Contains(value, "momentum"):
		return "event"
	default:
		return value
	}
}

func normalizeStructure(value string, legs []model.TradeLeg) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != "" {
		return value
	}
	if len(legs) > 1 {
		return "custom_combo"
	}
	return "single"
}

func normalizeExchange(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "SMART"
	}
	return value
}

func truncateForLog(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func (d *Desk) buildResearchPrompt(opp *model.Opportunity, sig signal.Signal, marketCtx *model.MarketContext, compact bool) string {
	var sb strings.Builder
	if compact {
		sb.WriteString(d.compactPrefix + "\n\n")
	}
	_, _ = fmt.Fprintf(&sb, "Opportunity (score: %.0f, urgency: %.2f, category: %s):\n", opp.Score, opp.Urgency, opp.Category)
	_, _ = fmt.Fprintf(&sb, "Instruments: %v\n", instrumentNames(opp.Instruments))
	_, _ = fmt.Fprintf(&sb, "Direction: %s\n", opp.Direction)
	_, _ = fmt.Fprintf(&sb, "Signal IDs: %v\n", opp.SignalIDs)

	sb.WriteString("\nSignal snapshot:\n")
	sb.WriteString(formatSignalForResearch(sig, compact))

	if opp.EvidenceMeta != nil {
		sb.WriteString("\nEvidence quality:\n")
		sb.WriteString(formatEvidenceMetadataForResearch(opp.EvidenceMeta, compact))
		sb.WriteString("\nUse this to calibrate conviction. Contradictory or weak evidence should reduce confidence materially.")
	}

	if opp.CascadeInfo != nil {
		_, _ = fmt.Fprintf(&sb, "\n\nCascade detected:\n  Source domain: %s\n  Target gaps: %v\n  Confidence: %.2f",
			opp.CascadeInfo.SourceDomain, opp.CascadeInfo.TargetGaps, opp.CascadeInfo.Confidence)
	}

	if marketSummary := marketcontext.FormatForPrompt(marketCtx); marketSummary != "" {
		sb.WriteString("\n\nMarket context:\n" + marketSummary + "\nUse this snapshot to judge whether the setup is genuinely surprising or already priced in.")
	}

	return sb.String()
}

func formatSignalForResearch(sig signal.Signal, compact bool) string {
	contentLimit := 900
	relatedLimit := 6
	entityLimit := 8
	if compact {
		contentLimit = 420
		relatedLimit = 3
		entityLimit = 4
	}
	return institutional.BuildSignalContext(sig, institutional.SignalContextOptions{
		Compact:              compact,
		Indent:               "  ",
		ContentLimit:         contentLimit,
		RelatedLimit:         relatedLimit,
		EntityLimit:          entityLimit,
		IncludeEvidence:      false,
		IncludeInstitutional: true,
	})
}

func formatEvidenceMetadataForResearch(meta *evidence.Metadata, compact bool) string {
	return institutional.BuildEvidenceContext(meta, institutional.EvidenceContextOptions{
		Compact: compact,
		Indent:  "  ",
	})
}

func minResearchRetryTokens(maxTokens int) int {
	if researchRetryMaxTokens > 0 && researchRetryMaxTokens < maxTokens {
		return researchRetryMaxTokens
	}
	if maxTokens > 384 {
		return 384
	}
	return maxTokens
}
