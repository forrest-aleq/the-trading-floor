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
	return &Desk{
		log:            slog.Default().With("component", "research"),
		llm:            llmRouter,
		minConviction:  minConviction,
		selectedModel:  researchSelectedModel(),
		responseMode:   detectStructuredResponseMode(os.Getenv("RESEARCH_RESPONSE_MODE"), researchSelectedModel()),
		retryModel:     structuredRetryModel("RESEARCH_RETRY_MODEL", structuredCompilerModel("RESEARCH_COMPILER_MODEL"), researchSelectedModel()),
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
	researchThoughtTimeout    = readStructuredDurationEnv("RESEARCH_THOUGHT_TIMEOUT", 30*time.Second)
	researchRetryTimeout      = readStructuredDurationEnv("RESEARCH_RETRY_TIMEOUT", 20*time.Second)
	researchRetryMaxTokens    = readStructuredIntEnv("RESEARCH_RETRY_MAX_TOKENS", 384)
	researchCompilerTimeout   = readStructuredDurationEnv("RESEARCH_COMPILER_TIMEOUT", 35*time.Second)
	researchCompilerMaxTokens = readStructuredIntEnv("RESEARCH_COMPILER_MAX_TOKENS", 900)
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
	prompt := d.buildResearchPrompt(opp, sig, marketCtx, false)
	compactPrompt := d.buildResearchPrompt(opp, sig, marketCtx, true)

	resp, err := d.askResearchWithFallbackMode(ctx, prompt, compactPrompt)
	if err != nil {
		return Investigation{Reason: "llm_error"}, fmt.Errorf("research LLM error: %w", err)
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
		d.log.Warn("research JSON extraction failed",
			"error", err,
			"response_len", len(resp),
			"response_excerpt", truncateForLog(resp, 320),
		)
		return Investigation{Reason: "json_extraction"}, fmt.Errorf("research JSON extraction: %w", err)
	}
	cleaned = enrichResearchJSON(cleaned, opp, marketCtx)

	if err := llm.ValidateJSONFields(cleaned, []string{"instrument", "direction", "entry_price"}); err != nil {
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

	killRules := parseKillRules(result.KillRules, result.StopLoss)
	legs := normalizeTradeLegs(result.Legs, result.EntryPrice)
	structure := normalizeStructure(result.Structure, legs)
	primary := model.Instrument{
		Symbol:   result.Instrument.Symbol,
		SecType:  result.Instrument.SecType,
		Currency: result.Instrument.Currency,
		Exchange: normalizeExchange(result.Instrument.Exchange),
		Expiry:   result.Instrument.Expiry,
		Strike:   result.Instrument.Strike,
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
		Conviction:         normalizeResearchConviction(result.Conviction, opp),
		Health:             0.85, // Initial health
		Evidence:           evidence,
		CounterArgs:        result.CounterArgs,
		EntryPrice:         result.EntryPrice,
		TargetPrice:        result.TargetPrice,
		StopLoss:           result.StopLoss,
		PositionSize:       normalizePositionSizePct(result.PositionSizePct),
		TimeHorizon:        time.Duration(result.TimeHorizonHours) * time.Hour,
		KillRules:          killRules,
		Status:             model.ThesisEmbryo,
		EvidenceMeta:       opp.EvidenceMeta.Clone(),
		MarketContext:      marketCtx,
		SurpriseAssessment: buildSurpriseAssessment(result),
		CreatedAt:          time.Now(),
	}

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

func (d *Desk) askResearchWithFallbackMode(ctx context.Context, prompt, compactPrompt string) (string, error) {
	if d.responseMode == structuredResponseModeThought {
		thoughtPrompt := addTerminalJSONContract(d.thoughtPrefix + "\n\n" + d.systemPrompt)
		primaryCtx, cancel := withStructuredTimeout(ctx, researchThoughtTimeout)
		resp, err := d.llm.AskWithLimit(primaryCtx, llm.TierAnalysis, thoughtPrompt, prompt, researchMaxTokens, 0.2)
		cancel()
		if err != nil {
			retryCtx, retryCancel := withStructuredTimeout(ctx, researchRetryTimeout)
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

		retryCtx, retryCancel := withStructuredTimeout(ctx, researchRetryTimeout)
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
	return d.llm.AskJSONWithLimit(ctx, llm.TierAnalysis, d.systemPrompt, prompt, researchMaxTokens, 0.2)
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
	compileCtx, cancel := context.WithTimeout(ctx, researchCompilerTimeout)
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
		Symbol   string  `json:"symbol"`
		SecType  string  `json:"sec_type"`
		Currency string  `json:"currency"`
		Exchange string  `json:"exchange"`
		Expiry   string  `json:"expiry"`
		Strike   float64 `json:"strike"`
		Right    string  `json:"right"`
	} `json:"instrument"`
	Legs []struct {
		Instrument struct {
			Symbol   string  `json:"symbol"`
			SecType  string  `json:"sec_type"`
			Currency string  `json:"currency"`
			Exchange string  `json:"exchange"`
			Expiry   string  `json:"expiry"`
			Strike   float64 `json:"strike"`
			Right    string  `json:"right"`
		} `json:"instrument"`
		Direction  string  `json:"direction"`
		Ratio      float64 `json:"ratio"`
		EntryPrice float64 `json:"entry_price"`
	} `json:"legs"`
	Direction          string  `json:"direction"`
	EntryPrice         float64 `json:"entry_price"`
	TargetPrice        float64 `json:"target_price"`
	StopLoss           float64 `json:"stop_loss"`
	Conviction         float64 `json:"conviction"`
	TimeHorizonHours   int     `json:"time_horizon_hours"`
	PositionSizePct    float64 `json:"position_size_pct"`
	Strategy           string  `json:"strategy"`
	SurpriseAssessment struct {
		TruthScore        float64 `json:"truth_score"`
		NoveltyScore      float64 `json:"novelty_score"`
		PricedInScore     float64 `json:"priced_in_score"`
		ReactionGapScore  float64 `json:"reaction_gap_score"`
		UnmovedAssetScore float64 `json:"unmoved_asset_score"`
		Summary           string  `json:"summary"`
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
		Symbol   string  `json:"symbol"`
		SecType  string  `json:"sec_type"`
		Currency string  `json:"currency"`
		Exchange string  `json:"exchange"`
		Expiry   string  `json:"expiry"`
		Strike   float64 `json:"strike"`
		Right    string  `json:"right"`
	} `json:"instrument"`
	Direction  string  `json:"direction"`
	Ratio      float64 `json:"ratio"`
	EntryPrice float64 `json:"entry_price"`
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
		ratio := leg.Ratio
		if ratio <= 0 {
			ratio = 1
		}
		entryPrice := leg.EntryPrice
		if entryPrice <= 0 {
			entryPrice = fallbackEntry
		}
		inst := normalizeResearchInstrument(model.Instrument{
			Symbol:   leg.Instrument.Symbol,
			SecType:  leg.Instrument.SecType,
			Currency: leg.Instrument.Currency,
			Exchange: normalizeExchange(leg.Instrument.Exchange),
			Expiry:   leg.Instrument.Expiry,
			Strike:   leg.Instrument.Strike,
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
	if shouldDowngradeStaleDerivative(inst) {
		inst = downgradeDerivativeToUnderlying(inst)
	}
	return inst
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

	if !hasInstrumentPayload(payload["instrument"]) && opp != nil && len(opp.Instruments) > 0 {
		payload["instrument"] = instrumentPayload(opp.Instruments[0])
	}
	if strings.TrimSpace(fmt.Sprint(payload["direction"])) == "" && opp != nil && opp.Direction != "" {
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
		TruthScore:        clampUnit(result.SurpriseAssessment.TruthScore),
		NoveltyScore:      clampUnit(result.SurpriseAssessment.NoveltyScore),
		PricedInScore:     clampUnit(result.SurpriseAssessment.PricedInScore),
		ReactionGapScore:  clampUnit(result.SurpriseAssessment.ReactionGapScore),
		UnmovedAssetScore: clampUnit(result.SurpriseAssessment.UnmovedAssetScore),
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
	sb.WriteString(fmt.Sprintf("Opportunity (score: %.0f, urgency: %.2f, category: %s):\n", opp.Score, opp.Urgency, opp.Category))
	sb.WriteString(fmt.Sprintf("Instruments: %v\n", instrumentNames(opp.Instruments)))
	sb.WriteString(fmt.Sprintf("Direction: %s\n", opp.Direction))
	sb.WriteString(fmt.Sprintf("Signal IDs: %v\n", opp.SignalIDs))

	sb.WriteString("\nSignal snapshot:\n")
	sb.WriteString(formatSignalForResearch(sig, compact))

	if opp.EvidenceMeta != nil {
		sb.WriteString("\nEvidence quality:\n")
		sb.WriteString(formatEvidenceMetadataForResearch(opp.EvidenceMeta, compact))
		sb.WriteString("\nUse this to calibrate conviction. Contradictory or weak evidence should reduce confidence materially.")
	}

	if opp.CascadeInfo != nil {
		sb.WriteString(fmt.Sprintf("\n\nCascade detected:\n  Source domain: %s\n  Target gaps: %v\n  Confidence: %.2f",
			opp.CascadeInfo.SourceDomain, opp.CascadeInfo.TargetGaps, opp.CascadeInfo.Confidence))
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
