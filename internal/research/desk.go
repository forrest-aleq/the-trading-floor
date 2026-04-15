package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/marketcontext"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

// Desk orchestrates thesis formation through the trio conversation
type Desk struct {
	log           *slog.Logger
	llm           *llm.Router
	minConviction float64
	marketContext *marketcontext.Service
	selectedModel string
	responseMode  structuredResponseMode
	compilerModel string
}

func NewDesk(llmRouter *llm.Router, minConviction float64) *Desk {
	if minConviction == 0 {
		minConviction = 0.65
	}
	return &Desk{
		log:           slog.Default().With("component", "research"),
		llm:           llmRouter,
		minConviction: minConviction,
		selectedModel: researchSelectedModel(),
		responseMode:  detectStructuredResponseMode(os.Getenv("RESEARCH_RESPONSE_MODE"), researchSelectedModel()),
		compilerModel: structuredCompilerModel("RESEARCH_COMPILER_MODEL"),
	}
}

func (d *Desk) SetMarketContextService(service *marketcontext.Service) {
	d.marketContext = service
}

const researchPrompt = `You are a trading research desk. Given this opportunity, build a rigorous trading thesis.

You must determine:
1. STRUCTURE: single, bull_call_spread, or bear_put_spread
2. INSTRUMENT / LEGS: What exactly to trade. Use a single instrument for simple trades. Use explicit legs only for debit vertical spreads.
2. DIRECTION: Long or short
3. ENTRY: Target entry price
4. TARGET: Price target (where to take profit)
5. STOP: Stop loss level
6. CONVICTION: 0.0-1.0 how confident you are
7. TIME HORIZON: How long should this trade be held (hours, days, weeks)
8. EVIDENCE: What supports this thesis (list 3-5 pieces of evidence)
9. COUNTER ARGUMENTS: What could go wrong (list 2-3 risks)
10. KILL RULES: Conditions that would invalidate the thesis
11. SURPRISE ASSESSMENT: score how novel and underpriced this setup is

Think like Bill Ackman. Deep conviction requires deep analysis. Don't trade unless you have an edge.
If you choose a spread, it must be a defined-risk debit vertical:
- bull_call_spread = long lower-strike call + short higher-strike call, same expiry
- bear_put_spread = long higher-strike put + short lower-strike put, same expiry
Do not propose condors, calendars, straddles, pairs, baskets, naked shorts, or undefined-risk structures yet.

Respond in JSON:
{
  "structure": "single|bull_call_spread|bear_put_spread",
  "instrument": {"symbol": "...", "sec_type": "STK|OPT|FUT|CASH", "currency": "USD", "exchange": "SMART", "expiry": "", "strike": 0, "right": ""},
  "legs": [
    {
      "instrument": {"symbol": "...", "sec_type": "STK|OPT|FUT|CASH", "currency": "USD", "exchange": "SMART", "expiry": "", "strike": 0, "right": ""},
      "direction": "long|short",
      "ratio": 1,
      "entry_price": 0.0
    }
  ],
  "direction": "long",
  "entry_price": 0.0,
  "target_price": 0.0,
  "stop_loss": 0.0,
  "conviction": 0.0,
  "time_horizon_hours": 0,
  "position_size_pct": 0.0,
  "strategy": "scalper|event|macro|fundamental|contrarian|tail",
  "surprise_assessment": {
    "truth_score": 0.0,
    "novelty_score": 0.0,
    "priced_in_score": 0.0,
    "reaction_gap_score": 0.0,
    "unmoved_asset_score": 0.0,
    "summary": ""
  },
  "evidence": ["...", "..."],
  "counter_args": ["...", "..."],
  "kill_rules": [{"condition": "...", "threshold": 0.0, "action": "close|reduce|alert"}],
  "reasoning": "..."
}`

const researchMaxTokens = 1024
const researchCompilerTimeout = 20 * time.Second
const researchCompilerMaxTokens = 1200

const researchThoughtPrefix = `Do not restate the request, schema, or instructions.
Think if useful, but keep it concise.
You must end with exactly one JSON object matching the schema below.`

const researchCompilerPrompt = `You are a trading thesis compiler.
You will receive the original research task and a freeform reasoning transcript from a trading research desk.
Return one final JSON object only. No prose, no markdown, no thinking.

If the transcript does not support a valid trade thesis, return a conservative thesis with low conviction and narrow position sizing, but still satisfy the schema.

JSON schema:
{
  "structure": "single|bull_call_spread|bear_put_spread",
  "instrument": {"symbol": "...", "sec_type": "STK|OPT|FUT|CASH", "currency": "USD", "exchange": "SMART", "expiry": "", "strike": 0, "right": ""},
  "legs": [
    {
      "instrument": {"symbol": "...", "sec_type": "STK|OPT|FUT|CASH", "currency": "USD", "exchange": "SMART", "expiry": "", "strike": 0, "right": ""},
      "direction": "long|short",
      "ratio": 1,
      "entry_price": 0.0
    }
  ],
  "direction": "long",
  "entry_price": 0.0,
  "target_price": 0.0,
  "stop_loss": 0.0,
  "conviction": 0.0,
  "time_horizon_hours": 0,
  "position_size_pct": 0.0,
  "strategy": "scalper|event|macro|fundamental|contrarian|tail",
  "surprise_assessment": {
    "truth_score": 0.0,
    "novelty_score": 0.0,
    "priced_in_score": 0.0,
    "reaction_gap_score": 0.0,
    "unmoved_asset_score": 0.0,
    "summary": ""
  },
  "evidence": ["...", "..."],
  "counter_args": ["...", "..."],
  "kill_rules": [{"condition": "...", "threshold": 0.0, "action": "close|reduce|alert"}],
  "reasoning": "..."
}`

// Investigate takes an opportunity and produces a thesis
func (d *Desk) Investigate(ctx context.Context, opp *model.Opportunity, sig signal.Signal, deskID string) (*model.Thesis, error) {
	prompt := fmt.Sprintf("Opportunity (score: %.0f, urgency: %.2f, category: %s):\n\nInstruments: %v\nDirection: %s\nSignal IDs: %v",
		opp.Score, opp.Urgency, opp.Category,
		instrumentNames(opp.Instruments), opp.Direction, opp.SignalIDs,
	)
	if opp.EvidenceMeta != nil {
		prompt += fmt.Sprintf(
			"\n\nEvidence quality:\n  Source trust: %.2f\n  Source tier/type: %s / %s\n  Source lineage: %s / %s\n  Original language: %s\n  Origin region: %s\n  Translation: provider=%s confidence=%.2f\n  Historical lead time: avg %.2fh across %d narratives (score %.2f)\n  Freshness: %s (age %.1fh, window %.1fh)\n  Distinct sources: %d\n  Distinct owner groups: %d\n  Distinct languages: %d\n  Has primary source: %t\n  Contradictions: %d (%s)\n  Evidence score: %.2f\nUse this to calibrate conviction. Contradictory or weak evidence should reduce confidence materially.",
			opp.EvidenceMeta.SourceTrust,
			opp.EvidenceMeta.SourceTier,
			opp.EvidenceMeta.SourceType,
			opp.EvidenceMeta.SourceDomain,
			opp.EvidenceMeta.SourceOwnerGroup,
			opp.EvidenceMeta.OriginalLanguage,
			opp.EvidenceMeta.OriginRegion,
			opp.EvidenceMeta.TranslationProvider,
			opp.EvidenceMeta.TranslationConfidence,
			opp.EvidenceMeta.LeadTimeAverageHours,
			opp.EvidenceMeta.LeadTimeObservations,
			opp.EvidenceMeta.LeadTimeScore,
			opp.EvidenceMeta.FreshnessStatus,
			opp.EvidenceMeta.FreshnessAgeHours,
			opp.EvidenceMeta.FreshnessWindowHours,
			opp.EvidenceMeta.DistinctSources,
			opp.EvidenceMeta.DistinctOwnerGroups,
			opp.EvidenceMeta.DistinctLanguages,
			opp.EvidenceMeta.HasPrimarySource,
			opp.EvidenceMeta.ContradictionCount,
			opp.EvidenceMeta.ContradictionSeverity,
			opp.EvidenceMeta.EvidenceScore,
		)
		if vector := opp.EvidenceMeta.ConfidenceVector; vector != nil && vector.Present() {
			prompt += fmt.Sprintf(
				"\n  Confidence vector: fact=%.2f novelty=%.2f market_map=%.2f expression=%.2f execution=%.2f competence=%.2f",
				vector.FactConfidence,
				vector.NoveltyConfidence,
				vector.MarketMappingConfidence,
				vector.ExpressionConfidence,
				vector.ExecutionConfidence,
				vector.CompetenceConfidence,
			)
		}
	}

	if opp.CascadeInfo != nil {
		prompt += fmt.Sprintf("\n\nCascade detected:\n  Source domain: %s\n  Target gaps: %v\n  Confidence: %.2f",
			opp.CascadeInfo.SourceDomain, opp.CascadeInfo.TargetGaps, opp.CascadeInfo.Confidence)
	}

	var marketCtx *model.MarketContext
	if d.marketContext != nil {
		marketCtx = d.marketContext.BuildOpportunityContext(opp, sig)
		if marketSummary := marketcontext.FormatForPrompt(marketCtx); marketSummary != "" {
			prompt += "\n\nMarket context:\n" + marketSummary + "\nUse this snapshot to judge whether the setup is genuinely surprising or already priced in."
		}
	}

	resp, err := d.askResearchWithFallbackMode(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("research LLM error: %w", err)
	}

	cleaned, err := extractStructuredJSON(resp)
	if err != nil {
		if d.compilerModel != "" {
			if compiled, compileErr := d.compileResearchJSON(ctx, prompt, resp); compileErr == nil {
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
		return nil, fmt.Errorf("research JSON extraction: %w", err)
	}

	if err := llm.ValidateJSONFields(cleaned, []string{"instrument", "direction", "entry_price", "conviction", "strategy"}); err != nil {
		return nil, fmt.Errorf("research response validation: %w", err)
	}

	var result researchResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("research parse error: %w", err)
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
	if primary.Symbol == "" && len(legs) > 0 {
		primary = legs[0].Instrument
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
		Conviction:         normalizeConviction(result.Conviction),
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

	return thesis, nil
}

func (d *Desk) askResearchWithFallbackMode(ctx context.Context, prompt string) (string, error) {
	systemPrompt := researchPrompt
	if d.responseMode == structuredResponseModeThought {
		systemPrompt = addTerminalJSONContract(researchThoughtPrefix + "\n\n" + researchPrompt)
		return d.llm.AskWithLimit(ctx, llm.TierAnalysis, systemPrompt, prompt, researchMaxTokens, 0.2)
	}
	return d.llm.AskJSONWithLimit(ctx, llm.TierAnalysis, systemPrompt, prompt, researchMaxTokens, 0.2)
}

func (d *Desk) compileResearchJSON(ctx context.Context, originalPrompt, rawResponse string) (string, error) {
	compileCtx, cancel := context.WithTimeout(ctx, researchCompilerTimeout)
	defer cancel()

	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: researchCompilerPrompt},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Original research task:\n%s\n\nResearch reasoning transcript:\n%s", originalPrompt, truncateForCompiler(rawResponse, 2400))},
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
		legs = append(legs, model.TradeLeg{
			Instrument: model.Instrument{
				Symbol:   leg.Instrument.Symbol,
				SecType:  leg.Instrument.SecType,
				Currency: leg.Instrument.Currency,
				Exchange: normalizeExchange(leg.Instrument.Exchange),
				Expiry:   leg.Instrument.Expiry,
				Strike:   leg.Instrument.Strike,
				Right:    leg.Instrument.Right,
			},
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

func normalizePositionSizePct(value float64) float64 {
	switch {
	case value > 1 && value <= 100:
		value /= 100
	case value > 100:
		value = 1
	case value < 0:
		value = 0
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
