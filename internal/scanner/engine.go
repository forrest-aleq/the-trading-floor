package scanner

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
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

// Engine evaluates signals for tradeable opportunities using the speed-tier LLM
type Engine struct {
	log      *slog.Logger
	llm      *llm.Router
	minScore float64 // Minimum score to pass (0-100)
}

var (
	scannerRequestTimeout    = readDurationEnv("SCANNER_REQUEST_TIMEOUT", 15*time.Second)
	scannerMaxTokens         = readIntEnv("SCANNER_MAX_TOKENS", 128)
	scannerCompactMaxTokens  = readIntEnv("SCANNER_COMPACT_MAX_TOKENS", 96)
	scannerContentLimit      = readIntEnv("SCANNER_CONTENT_LIMIT", 500)
	scannerCompactContentMax = readIntEnv("SCANNER_COMPACT_CONTENT_LIMIT", 220)
	scannerStaleSignalAge    = readDurationEnv("SCANNER_STALE_SIGNAL_AGE", 6*time.Hour)
)

func NewEngine(llmRouter *llm.Router, minScore float64) *Engine {
	if minScore == 0 {
		minScore = 70 // Default: aggressive filter — most signals should be rejected
	}
	return &Engine{
		log:      slog.Default().With("component", "scanner"),
		llm:      llmRouter,
		minScore: minScore,
	}
}

const scannerPrompt = `You are a trading signal scanner. Output one final JSON object only.
Do not include chain-of-thought, thinking tags, XML, markdown, or any explanatory preamble.
Your DEFAULT response should be tradeable: false. Most signals are noise.

Only mark tradeable: true if ALL of these are met:
1. There is a SPECIFIC, actionable trade thesis (not vague commentary)
2. You can name EXACT instruments to trade (tickers, not sectors)
3. There is a clear catalyst with a defined time window
4. The signal contains hard data or a confirmed event (not rumor or speculation)
5. The expected move is large enough to overcome transaction costs
6. Cross-source corroboration should increase confidence; isolated single-source noise should usually fail

If in doubt, set tradeable: false. We lose nothing by passing. We lose real money by acting on noise.

Respond in JSON:
{
  "tradeable": true/false,
  "score": 0-100,
  "instruments": [{"symbol": "AAPL", "sec_type": "STK", "currency": "USD"}],
  "direction": "long" or "short",
  "urgency": 0.0-1.0,
  "category": "geopolitical|macro|corporate|flows|tail|volatility|sector|systematic",
  "reasoning": "brief explanation, 12 words max"
}`

// Evaluate checks if a signal contains a tradeable opportunity
func (e *Engine) Evaluate(ctx context.Context, sig signal.Signal, domain string) (*model.Opportunity, bool) {
	if skip, reason := shouldSkipSignal(sig); skip {
		e.log.Debug("scanner skipped by deterministic prefilter",
			"signal_id", sig.ID,
			"reason", reason,
			"source", sig.Source,
			"category", sig.Category,
			"type", sig.Type,
			"urgency", sig.Urgency,
			"source_trust", evidenceTrust(sig),
			"evidence_score", evidenceScore(sig),
		)
		return nil, false
	}

	prompts := []struct {
		name      string
		content   string
		maxTokens int
	}{
		{
			name:      "default",
			content:   buildPrompt(domain, formatSignalWithLimit(sig, scannerContentLimit, 4, 12)),
			maxTokens: scannerMaxTokens,
		},
		{
			name:      "compact",
			content:   buildPrompt(domain, formatSignalWithLimit(sig, scannerCompactContentMax, 2, 6)),
			maxTokens: scannerCompactMaxTokens,
		},
	}

	var resp string
	var err error
	for i, candidate := range prompts {
		reqCtx, cancel := context.WithTimeout(ctx, scannerRequestTimeout)
		resp, err = e.llm.AskJSONWithLimit(reqCtx, llm.TierSpeed, scannerPrompt, candidate.content, candidate.maxTokens, 0.1)
		cancel()
		if err == nil {
			break
		}
		if i == len(prompts)-1 || !isContextWindowError(err) {
			e.log.Warn("scanner LLM error",
				"error", err,
				"signal_id", sig.ID,
				"attempt", candidate.name,
				"prompt_chars", len(candidate.content),
				"max_tokens", candidate.maxTokens,
			)
			return nil, false
		}
		e.log.Warn("scanner request exceeded context window, retrying compact prompt",
			"signal_id", sig.ID,
			"attempt", candidate.name,
			"prompt_chars", len(candidate.content),
			"max_tokens", candidate.maxTokens,
		)
	}

	cleaned, err := llm.ExtractJSON(resp)
	if err != nil {
		e.log.Warn("scanner JSON extraction failed",
			"error", err,
			"signal_id", sig.ID,
			"response_len", len(resp),
			"response_excerpt", truncateForPrompt(resp, 240),
		)
		return nil, false
	}

	var result scanResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		e.log.Warn("scanner parse error", "error", err, "response", cleaned)
		return nil, false
	}

	if !result.Tradeable || result.Score < e.minScore {
		return nil, false
	}

	// Build instruments
	instruments := make([]model.Instrument, len(result.Instruments))
	for i, inst := range result.Instruments {
		instruments[i] = model.Instrument{
			Symbol:   inst.Symbol,
			SecType:  inst.SecType,
			Currency: inst.Currency,
			Exchange: "SMART", // IBKR smart routing default
		}
		if instruments[i].Currency == "" {
			instruments[i].Currency = "USD"
		}
		if instruments[i].SecType == "" {
			instruments[i].SecType = "STK"
		}
	}

	direction := model.Long
	if result.Direction == "short" {
		direction = model.Short
	}

	opp := &model.Opportunity{
		ID:           uuid.New().String(),
		SignalIDs:    []string{sig.ID},
		Instruments:  instruments,
		Direction:    direction,
		Urgency:      result.Urgency,
		Score:        result.Score,
		Category:     result.Category,
		EvidenceMeta: sig.EvidenceMeta.Clone(),
		CreatedAt:    time.Now(),
	}

	e.log.Info("opportunity detected",
		"score", result.Score,
		"instruments", len(instruments),
		"direction", direction,
		"category", result.Category,
		"signal_source", sig.Source,
	)

	return opp, true
}

func shouldSkipSignal(sig signal.Signal) (bool, string) {
	if allowed, reason := sig.EvidenceGate(); !allowed {
		return true, reason
	}
	if !sig.Timestamp.IsZero() && time.Since(sig.Timestamp) > scannerStaleSignalAge {
		return true, "stale_signal_age"
	}
	if sig.Type == signal.TypeSocial && sig.Urgency < 0.5 && len(sig.CorroboratingSources) == 0 {
		return true, "low_signal_social_noise"
	}
	return false, ""
}

func readIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func readDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

type scanResult struct {
	Tradeable   bool    `json:"tradeable"`
	Score       float64 `json:"score"`
	Instruments []struct {
		Symbol   string `json:"symbol"`
		SecType  string `json:"sec_type"`
		Currency string `json:"currency"`
	} `json:"instruments"`
	Direction string  `json:"direction"`
	Urgency   float64 `json:"urgency"`
	Category  string  `json:"category"`
	Reasoning string  `json:"reasoning"`
}

// domainContext returns domain-specific scanner guidance to focus evaluation.
func domainContext(domain string) string {
	switch domain {
	case "geopolitical":
		return `Focus: supply-chain disruption, sanctions, regime change, military conflict, trade policy.
Look for: second-order effects on specific sectors/companies. Ignore vague geopolitical commentary.
Preferred instruments: affected commodities, defense stocks, supply-chain-exposed companies.`
	case "macro":
		return `Focus: interest rate changes, inflation data, employment reports, central bank actions, yield curve.
Look for: deviations from consensus expectations. Ignore data that matches expectations.
Preferred instruments: rate-sensitive ETFs (TLT, XLF), FX, index futures, sector rotations.`
	case "corporate":
		return `Focus: earnings surprises, insider transactions, M&A rumors, activist campaigns, SEC filings.
Look for: material non-public-equivalent signals (unusual filing patterns, insider buying clusters).
Preferred instruments: individual stocks, options around catalysts.`
	case "flows":
		return `Focus: unusual options activity, dark pool prints, short interest spikes, ETF flow anomalies.
Look for: large block trades, put/call ratio extremes, gamma exposure inflection points.
Preferred instruments: options (straddles, strangles around flow anomalies), stocks with positioning extremes.`
	case "tail":
		return `Focus: black swan indicators, systemic risk signals, credit stress, liquidity crises.
Look for: VIX term structure inversion, credit spread blowouts, correlation spikes, flash crash precursors.
Preferred instruments: VIX calls, deep OTM puts on indices, credit default proxies (HYG puts).`
	case "volatility":
		return `Focus: implied vs realized vol divergence, term structure anomalies, skew shifts, variance risk premium.
Look for: vol surface dislocations, event vol mispricing, cross-asset vol divergences.
Preferred instruments: VIX futures/options, straddles, calendar spreads, variance swaps (via options).`
	case "sector":
		return `Focus: sector rotation signals, relative strength shifts, thematic catalysts (FDA, regulations, tech cycles).
Look for: sector-specific catalysts with clear timeline. Ignore broad market noise.
Preferred instruments: sector ETFs, individual sector leaders/laggards, options on sector plays.`
	case "systematic":
		return `Focus: momentum breakouts, mean reversion setups, statistical anomalies, factor exposures.
Look for: quantitative signals — price/volume patterns, cross-sectional momentum, pairs divergence.
Preferred instruments: high-liquidity stocks and ETFs suitable for systematic entry/exit.`
	default:
		return ""
	}
}

func formatSignal(sig signal.Signal) string {
	return formatSignalWithLimit(sig, scannerContentLimit, 4, 12)
}

func formatSignalWithLimit(sig signal.Signal, contentLimit, relatedLimit, entityLimit int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Source: %s\n", sig.Source))
	sb.WriteString(fmt.Sprintf("Type: %s\n", sig.Type))
	sb.WriteString(fmt.Sprintf("Category: %s\n", sig.Category))
	sb.WriteString(fmt.Sprintf("Urgency: %.2f\n", sig.Urgency))
	if sig.ClusterID != "" {
		sb.WriteString(fmt.Sprintf("Cluster: %s\n", sig.ClusterID))
	}
	if len(sig.RelatedSignalIDs) > 0 {
		sb.WriteString(fmt.Sprintf("Related signals: %d (%s)\n", len(sig.RelatedSignalIDs), strings.Join(sampleStrings(sig.RelatedSignalIDs, relatedLimit), ", ")))
	}
	if len(sig.CorroboratingSources) > 0 {
		sb.WriteString(fmt.Sprintf("Corroborating sources: %s\n", strings.Join(sampleStrings(sig.CorroboratingSources, relatedLimit), ", ")))
	}
	if len(sig.CorroboratingEntities) > 0 {
		sb.WriteString(fmt.Sprintf("Corroborating entities: %s\n", strings.Join(sampleStrings(sig.CorroboratingEntities, relatedLimit), ", ")))
	}
	if sig.EvidenceMeta != nil {
		sb.WriteString(fmt.Sprintf("Source trust: %.2f\n", sig.EvidenceMeta.SourceTrust))
		if sig.EvidenceMeta.SourceTier != "" || sig.EvidenceMeta.SourceType != "" {
			sb.WriteString(fmt.Sprintf("Source quality: tier=%s type=%s\n", sig.EvidenceMeta.SourceTier, sig.EvidenceMeta.SourceType))
		}
		if sig.EvidenceMeta.SourceDomain != "" || sig.EvidenceMeta.SourceOwnerGroup != "" {
			sb.WriteString(fmt.Sprintf("Source lineage: domain=%s owner_group=%s\n", sig.EvidenceMeta.SourceDomain, sig.EvidenceMeta.SourceOwnerGroup))
		}
		if len(sig.EvidenceMeta.CorroboratingOwnerGroups) > 0 {
			sb.WriteString(fmt.Sprintf("Independent owner groups: %s\n", strings.Join(sampleStrings(sig.EvidenceMeta.CorroboratingOwnerGroups, relatedLimit), ", ")))
		}
		if sig.EvidenceMeta.FreshnessStatus != "" {
			sb.WriteString(fmt.Sprintf("Freshness: %s (age %.1fh / window %.1fh)\n", sig.EvidenceMeta.FreshnessStatus, sig.EvidenceMeta.FreshnessAgeHours, sig.EvidenceMeta.FreshnessWindowHours))
		}
		if sig.EvidenceMeta.ContradictionCount > 0 {
			sb.WriteString(fmt.Sprintf("Contradictions: %d (%s)\n", sig.EvidenceMeta.ContradictionCount, sig.EvidenceMeta.ContradictionSeverity))
		}
		sb.WriteString(fmt.Sprintf("Evidence score: %.2f\n", sig.EvidenceMeta.EvidenceScore))
	}
	if sig.Translated != "" {
		sb.WriteString(fmt.Sprintf("Content: %s\n", truncateForPrompt(sig.Translated, contentLimit)))
	} else if len(sig.Raw) > 0 {
		sb.WriteString(fmt.Sprintf("Content: %s\n", truncateForPrompt(string(sig.Raw), contentLimit)))
	}
	if len(sig.Entities) > 0 {
		entities := sig.Entities
		if len(entities) > entityLimit {
			entities = entities[:entityLimit]
		}
		names := make([]string, len(entities))
		for i, e := range entities {
			names[i] = e.Name
		}
		sb.WriteString(fmt.Sprintf("Entities: %s\n", strings.Join(names, ", ")))
	}
	return sb.String()
}

func evidenceTrust(sig signal.Signal) float64 {
	if sig.EvidenceMeta == nil {
		return 0
	}
	return sig.EvidenceMeta.SourceTrust
}

func evidenceScore(sig signal.Signal) float64 {
	if sig.EvidenceMeta == nil {
		return 0
	}
	return sig.EvidenceMeta.EvidenceScore
}

func sampleStrings(items []string, max int) []string {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

func truncateForPrompt(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}

func buildPrompt(domain, content string) string {
	domainGuide := domainContext(domain)
	prompt := fmt.Sprintf("Domain filter: %s\n", domain)
	if domainGuide != "" {
		prompt += fmt.Sprintf("\nDomain specialization:\n%s\n", domainGuide)
	}
	prompt += fmt.Sprintf("\nSignal:\n%s", content)
	return prompt
}

func isContextWindowError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "context size") ||
		strings.Contains(message, "context window") ||
		strings.Contains(message, "too many tokens") ||
		strings.Contains(message, "maximum context length")
}
