package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

// Engine evaluates signals for tradeable opportunities using the speed-tier LLM
type Engine struct {
	log           *slog.Logger
	llm           *llm.Router
	minScore      float64 // Minimum score to pass (0-100)
	selectedModel string
	responseMode  scannerResponseMode
	compilerModel string

	mu                   sync.Mutex
	llmUnavailableUntil  time.Time
	llmUnavailableReason string
	lastCooldownNoticeAt time.Time
}

var (
	scannerRequestTimeout         = readDurationEnv("SCANNER_REQUEST_TIMEOUT", 15*time.Second)
	scannerMaxTokens              = readIntEnv("SCANNER_MAX_TOKENS", 128)
	scannerCompactMaxTokens       = readIntEnv("SCANNER_COMPACT_MAX_TOKENS", 96)
	scannerThinkingRequestTimeout = readDurationEnv("SCANNER_THINKING_REQUEST_TIMEOUT", 12*time.Second)
	scannerThinkingMaxTokens      = readIntEnv("SCANNER_THINKING_MAX_TOKENS", 128)
	scannerThinkingCompactTokens  = readIntEnv("SCANNER_THINKING_COMPACT_MAX_TOKENS", 96)
	scannerCompilerTimeout        = readDurationEnv("SCANNER_COMPILER_TIMEOUT", 8*time.Second)
	scannerCompilerMaxTokens      = readIntEnv("SCANNER_COMPILER_MAX_TOKENS", 96)
	scannerContentLimit           = readIntEnv("SCANNER_CONTENT_LIMIT", 500)
	scannerCompactContentMax      = readIntEnv("SCANNER_COMPACT_CONTENT_LIMIT", 220)
	scannerStaleSignalAge         = readDurationEnv("SCANNER_STALE_SIGNAL_AGE", 6*time.Hour)
	scannerLLMCooldown            = readDurationEnv("SCANNER_LLM_UNAVAILABLE_COOLDOWN", 20*time.Second)
)

func NewEngine(llmRouter *llm.Router, minScore float64) *Engine {
	if minScore == 0 {
		minScore = 70 // Default: aggressive filter — most signals should be rejected
	}
	selectedModel := scannerSelectedModel()
	return &Engine{
		log:           slog.Default().With("component", "scanner"),
		llm:           llmRouter,
		minScore:      minScore,
		selectedModel: selectedModel,
		responseMode:  detectScannerResponseMode(selectedModel),
		compilerModel: strings.TrimSpace(os.Getenv("SCANNER_COMPILER_MODEL")),
	}
}

const scannerStructuredPrompt = `You are a trading signal scanner. Output one final JSON object only.
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

const scannerStructuredFastPrompt = `Return one final JSON object only.
Default to tradeable=false.
Only set tradeable=true if there is a specific catalyst, exact instruments, confirmed evidence, and enough expected move.

JSON:
{
  "tradeable": true/false,
  "score": 0-100,
  "instruments": [{"symbol": "AAPL", "sec_type": "STK", "currency": "USD"}],
  "direction": "long|short|none",
  "urgency": 0.0-1.0,
  "category": "geopolitical|macro|corporate|flows|tail|volatility|sector|systematic",
  "reasoning": "brief explanation, 12 words max"
}`

const scannerThoughtPrompt = `You are a trading signal scanner. Most signals are noise.
Do not restate the request, rubric, or output schema.
Think briefly if useful, but keep it to at most 3 short bullets.
If the signal is obviously not tradeable, skip the essay and go straight to the terminal decision block.
You MUST end with exactly one terminal decision block.

Only mark tradeable: true if ALL of these are met:
1. There is a SPECIFIC, actionable trade thesis (not vague commentary)
2. You can name EXACT instruments to trade (tickers, not sectors)
3. There is a clear catalyst with a defined time window
4. The signal contains hard data or a confirmed event (not rumor or speculation)
5. The expected move is large enough to overcome transaction costs
6. Cross-source corroboration should increase confidence; isolated single-source noise should usually fail

If in doubt, set tradeable: false. We lose nothing by passing. We lose real money by acting on noise.

Final block format:
FINAL_DECISION
tradeable: true/false
score: 0-100
instruments: SYMBOL[:SECTYPE[:CURRENCY]], SYMBOL[:SECTYPE[:CURRENCY]] or none
direction: long|short|none
urgency: 0.0-1.0
category: geopolitical|macro|corporate|flows|tail|volatility|sector|systematic
reasoning: brief explanation, 12 words max
END_FINAL_DECISION

If you are running out of room, output the final block immediately.
Do not omit the final block.`

const scannerCompilerPrompt = `You are a trading decision compiler.
You will receive a trading signal and a scanner's freeform reasoning transcript.
Return one final JSON object only. No prose, no markdown, no thinking.

Default to tradeable=false if the reasoning or evidence is ambiguous.

JSON schema:
{
  "tradeable": true/false,
  "score": 0-100,
  "instruments": [{"symbol": "AAPL", "sec_type": "STK", "currency": "USD"}],
  "direction": "long" or "short" or "none",
  "urgency": 0.0-1.0,
  "category": "geopolitical|macro|corporate|flows|tail|volatility|sector|systematic",
  "reasoning": "brief explanation, 12 words max"
}`

type scannerResponseMode string

const (
	scannerResponseModeStructured scannerResponseMode = "structured_json"
	scannerResponseModeThought    scannerResponseMode = "thought_block"
)

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

	if until, reason, shouldLog := e.llmCooldown(time.Now().UTC()); !until.IsZero() {
		if shouldLog {
			e.log.Warn("scanner skipping LLM requests during backend cooldown",
				"retry_at", until,
				"reason", reason,
			)
		}
		return nil, false
	}

	requestCfg := e.requestConfig()
	prompts := []struct {
		name      string
		content   string
		maxTokens int
	}{
		{
			name:      "default",
			content:   buildPrompt(domain, formatSignalWithLimit(sig, scannerContentLimit, 4, 12)),
			maxTokens: requestCfg.maxTokens,
		},
		{
			name:      "compact",
			content:   buildPrompt(domain, formatSignalWithLimit(sig, scannerCompactContentMax, 2, 6)),
			maxTokens: requestCfg.compactMaxTokens,
		},
	}

	var resp string
	var err error
	var usedPrompt string
	for i, candidate := range prompts {
		reqCtx, cancel := context.WithTimeout(ctx, requestCfg.timeout)
		resp, err = e.askScannerWithLimit(reqCtx, requestCfg.systemPrompt, candidate.content, candidate.maxTokens, 0.0, requestCfg.jsonMode)
		usedPrompt = candidate.content
		cancel()
		if err == nil {
			e.clearLLMCooldown()
			break
		}
		if isUnavailableLLMError(err) {
			e.tripLLMCooldown(time.Now().UTC(), err)
		}
		if requestCfg.allowStructuredFallback && isScannerTimeoutError(err) {
			fallbackResp, fallbackPrompt, fallbackErr := e.retryStructuredFallback(ctx, domain, sig)
			if fallbackErr == nil {
				e.log.Info("scanner structured fallback recovered decision",
					"signal_id", sig.ID,
					"prompt_chars", len(fallbackPrompt),
					"max_tokens", scannerCompactMaxTokens,
				)
				resp = fallbackResp
				usedPrompt = fallbackPrompt
				e.clearLLMCooldown()
				err = nil
				break
			}
			e.log.Warn("scanner structured fallback failed",
				"signal_id", sig.ID,
				"error", fallbackErr,
				"prompt_chars", len(candidate.content),
				"max_tokens", candidate.maxTokens,
			)
		}
		if i == len(prompts)-1 || !isScannerCompactRetryError(err) {
			if requestCfg.allowStructuredFallback && !isUnavailableLLMError(err) {
				fallbackResp, fallbackPrompt, fallbackErr := e.retryStructuredFallback(ctx, domain, sig)
				if fallbackErr == nil {
					e.log.Info("scanner structured fallback recovered decision",
						"signal_id", sig.ID,
						"prompt_chars", len(fallbackPrompt),
						"max_tokens", scannerCompactMaxTokens,
					)
					resp = fallbackResp
					usedPrompt = fallbackPrompt
					e.clearLLMCooldown()
					err = nil
					break
				}
				e.log.Warn("scanner structured fallback failed",
					"signal_id", sig.ID,
					"error", fallbackErr,
					"prompt_chars", len(candidate.content),
					"max_tokens", candidate.maxTokens,
				)
			}
			e.log.Warn("scanner LLM error",
				"error", err,
				"signal_id", sig.ID,
				"attempt", candidate.name,
				"prompt_chars", len(candidate.content),
				"max_tokens", candidate.maxTokens,
			)
			return nil, false
		}
		e.log.Warn("scanner request exceeded primary scan budget, retrying compact prompt",
			"signal_id", sig.ID,
			"attempt", candidate.name,
			"prompt_chars", len(candidate.content),
			"max_tokens", candidate.maxTokens,
		)
	}

	result, err := parseScanResponse(resp)
	if err != nil && requestCfg.allowCompilerFallback {
		if compiled, compileErr := e.compileScannerDecision(ctx, usedPrompt, resp); compileErr == nil {
			if parsed, parseErr := parseScanResponse(compiled); parseErr == nil {
				e.log.Info("scanner compiler recovered structured decision",
					"signal_id", sig.ID,
					"compiler_model", e.compilerModel,
				)
				result = parsed
				err = nil
			} else {
				err = parseErr
			}
		} else {
			e.log.Warn("scanner compiler fallback failed",
				"signal_id", sig.ID,
				"compiler_model", e.compilerModel,
				"error", compileErr,
			)
		}
	}
	if err != nil {
		e.log.Warn("scanner parse error",
			"error", err,
			"signal_id", sig.ID,
			"response_len", len(resp),
			"response_excerpt", truncateForPrompt(resp, 320),
		)
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

func (e *Engine) retryStructuredFallback(ctx context.Context, domain string, sig signal.Signal) (string, string, error) {
	prompt := buildCompactPrompt(domain, sig)
	reqCtx, cancel := context.WithTimeout(ctx, scannerRequestTimeout)
	defer cancel()

	resp, err := e.askScannerWithLimit(reqCtx, scannerStructuredFastPrompt, prompt, scannerCompactMaxTokens, 0.0, true)
	if err != nil {
		return "", prompt, err
	}
	return resp, prompt, nil
}

func (e *Engine) askScannerWithLimit(ctx context.Context, system, prompt string, maxTokens int, temperature float64, jsonMode bool) (string, error) {
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: prompt},
		},
		Model:       e.selectedModel,
		Tier:        llm.TierSpeed,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		JSONMode:    jsonMode,
	}
	resp, err := e.llm.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (e *Engine) compileScannerDecision(ctx context.Context, signalPrompt, rawResponse string) (string, error) {
	if e.compilerModel == "" {
		return "", fmt.Errorf("scanner compiler model not configured")
	}

	compileCtx, cancel := context.WithTimeout(ctx, scannerCompilerTimeout)
	defer cancel()

	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: scannerCompilerPrompt},
			{Role: llm.RoleUser, Content: buildScannerCompilerPrompt(signalPrompt, rawResponse)},
		},
		Model:       e.compilerModel,
		Tier:        llm.TierSpeed,
		MaxTokens:   scannerCompilerMaxTokens,
		Temperature: 0.0,
		JSONMode:    true,
	}
	resp, err := e.llm.Complete(compileCtx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

type scannerRequestConfig struct {
	systemPrompt            string
	jsonMode                bool
	timeout                 time.Duration
	maxTokens               int
	compactMaxTokens        int
	allowCompilerFallback   bool
	allowStructuredFallback bool
}

func (e *Engine) requestConfig() scannerRequestConfig {
	if e.responseMode == scannerResponseModeThought {
		return scannerRequestConfig{
			systemPrompt:            scannerThoughtPrompt,
			jsonMode:                false,
			timeout:                 scannerThinkingRequestTimeout,
			maxTokens:               scannerThinkingMaxTokens,
			compactMaxTokens:        scannerThinkingCompactTokens,
			allowCompilerFallback:   e.compilerModel != "",
			allowStructuredFallback: true,
		}
	}

	return scannerRequestConfig{
		systemPrompt:     scannerStructuredPrompt,
		jsonMode:         true,
		timeout:          scannerRequestTimeout,
		maxTokens:        scannerMaxTokens,
		compactMaxTokens: scannerCompactMaxTokens,
	}
}

func scannerSelectedModel() string {
	if model := strings.TrimSpace(os.Getenv("SCANNER_MODEL")); model != "" {
		return model
	}
	if model := strings.TrimSpace(os.Getenv("LLM_MODEL_SPEED")); model != "" {
		return model
	}
	return "qwen/qwen3-8b"
}

func buildScannerCompilerPrompt(signalPrompt, rawResponse string) string {
	return fmt.Sprintf("Original scanner task:\n%s\n\nScanner reasoning transcript:\n%s", signalPrompt, rawResponse)
}

func isScannerCompactRetryError(err error) bool {
	return isContextWindowError(err) || strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded")
}

func isScannerTimeoutError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded")
}

func detectScannerResponseMode(model string) scannerResponseMode {
	override := strings.ToLower(strings.TrimSpace(os.Getenv("SCANNER_RESPONSE_MODE")))
	switch override {
	case "json", "structured_json", "structured":
		return scannerResponseModeStructured
	case "thought", "thoughts", "thinking", "thought_block":
		return scannerResponseModeThought
	}
	if isThoughtFriendlyScannerModel(model) {
		return scannerResponseModeThought
	}
	return scannerResponseModeStructured
}

func isThoughtFriendlyScannerModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "qwen/") ||
		strings.Contains(model, "qwen3:") ||
		strings.Contains(model, "qwen2.5:") ||
		strings.HasPrefix(model, "qwen")
}

func extractFinalDecisionBlock(raw string) (string, error) {
	upper := strings.ToUpper(raw)
	start := strings.Index(upper, "FINAL_DECISION")
	end := strings.LastIndex(upper, "END_FINAL_DECISION")
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("terminal decision block missing")
	}
	block := strings.TrimSpace(raw[start+len("FINAL_DECISION") : end])
	if block == "" {
		return "", fmt.Errorf("terminal decision block empty")
	}
	return block, nil
}

func parseScanResponse(raw string) (scanResult, error) {
	if cleaned, err := llm.ExtractJSON(raw); err == nil {
		var result scanResult
		if err := json.Unmarshal([]byte(cleaned), &result); err == nil {
			return result, nil
		}
	}

	block, err := extractFinalDecisionBlock(raw)
	if err != nil {
		if recovered, ok := recoverConservativeThoughtReject(raw); ok {
			return recovered, nil
		}
		return scanResult{}, fmt.Errorf("no structured decision block found")
	}
	lines := strings.Split(block, "\n")
	fields := make(map[string]string, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		fields[key] = value
	}

	tradeable, err := strconv.ParseBool(strings.ToLower(fields["tradeable"]))
	if err != nil {
		return scanResult{}, fmt.Errorf("parse tradeable: %w", err)
	}
	score, err := strconv.ParseFloat(fields["score"], 64)
	if err != nil {
		return scanResult{}, fmt.Errorf("parse score: %w", err)
	}
	urgency, err := strconv.ParseFloat(fields["urgency"], 64)
	if err != nil {
		return scanResult{}, fmt.Errorf("parse urgency: %w", err)
	}

	result := scanResult{
		Tradeable: tradeable,
		Score:     score,
		Direction: strings.ToLower(strings.TrimSpace(fields["direction"])),
		Urgency:   urgency,
		Category:  strings.ToLower(strings.TrimSpace(fields["category"])),
		Reasoning: strings.TrimSpace(fields["reasoning"]),
	}
	if result.Direction == "" {
		result.Direction = "none"
	}

	instruments, err := parseScanInstruments(fields["instruments"])
	if err != nil {
		return scanResult{}, err
	}
	result.Instruments = instruments
	return result, nil
}

func parseScanInstruments(raw string) ([]struct {
	Symbol   string `json:"symbol"`
	SecType  string `json:"sec_type"`
	Currency string `json:"currency"`
}, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	instruments := make([]struct {
		Symbol   string `json:"symbol"`
		SecType  string `json:"sec_type"`
		Currency string `json:"currency"`
	}, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, ":")
		inst := struct {
			Symbol   string `json:"symbol"`
			SecType  string `json:"sec_type"`
			Currency string `json:"currency"`
		}{
			Symbol: strings.ToUpper(strings.TrimSpace(fields[0])),
		}
		if inst.Symbol == "" {
			return nil, fmt.Errorf("empty instrument symbol in %q", part)
		}
		if len(fields) > 1 {
			inst.SecType = strings.ToUpper(strings.TrimSpace(fields[1]))
		}
		if len(fields) > 2 {
			inst.Currency = strings.ToUpper(strings.TrimSpace(fields[2]))
		}
		instruments = append(instruments, inst)
	}

	return instruments, nil
}

func recoverConservativeThoughtReject(raw string) (scanResult, bool) {
	normalized := strings.ToLower(strings.ReplaceAll(raw, "\r", ""))
	if !strings.Contains(normalized, "thinking process") &&
		!strings.Contains(normalized, "analyze the request") &&
		!strings.Contains(normalized, "analyze the signal") {
		return scanResult{}, false
	}

	if containsAny(normalized,
		"this signal is tradeable",
		"this is tradeable",
		"tradeable opportunity",
		"looks actionable",
		"looks actionable as a trade",
		"appears actionable as a trade",
		"would trade this",
		"would take this trade",
		"recommend going long",
		"recommend going short",
		"meets all criteria",
		"passes all criteria",
	) {
		return scanResult{}, false
	}

	return scanResult{
		Tradeable: false,
		Score:     0,
		Direction: "none",
		Urgency:   0,
		Category:  inferThoughtCategory(normalized),
		Reasoning: inferThoughtRejectReason(raw),
	}, true
}

func inferThoughtCategory(normalized string) string {
	for _, category := range []string{
		"geopolitical",
		"macro",
		"corporate",
		"flows",
		"tail",
		"volatility",
		"sector",
		"systematic",
	} {
		if strings.Contains(normalized, "domain filter: "+category) ||
			strings.Contains(normalized, "category: "+category) ||
			strings.Contains(normalized, "category `"+category+"`") ||
			strings.Contains(normalized, "category \""+category+"\"") {
			return category
		}
	}
	return ""
}

func inferThoughtRejectReason(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		cleaned := normalizeThoughtLine(line)
		lower := strings.ToLower(cleaned)
		if cleaned == "" {
			continue
		}
		if containsAny(lower,
			"not tradeable",
			"not actionable",
			"does not meet",
			"fails the criteria",
			"fails multiple criteria",
			"no specific instrument",
			"no exact instrument",
			"no clear catalyst",
			"insufficient corroboration",
			"too vague",
			"not enough move",
			"no hard data",
			"lacks a precise directional setup",
			"lacks a specific thesis",
		) {
			return truncateForPrompt(cleaned, 96)
		}
	}
	return "incomplete thought trace defaulted to reject"
}

func normalizeThoughtLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-*0123456789. )`")
	return strings.Join(strings.Fields(line), " ")
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func (e *Engine) llmCooldown(now time.Time) (time.Time, string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.llmUnavailableUntil.IsZero() || !now.Before(e.llmUnavailableUntil) {
		return time.Time{}, "", false
	}

	shouldLog := e.lastCooldownNoticeAt.IsZero() || now.Sub(e.lastCooldownNoticeAt) >= 5*time.Second
	if shouldLog {
		e.lastCooldownNoticeAt = now
	}
	return e.llmUnavailableUntil, e.llmUnavailableReason, shouldLog
}

func (e *Engine) tripLLMCooldown(now time.Time, err error) {
	if scannerLLMCooldown <= 0 {
		return
	}

	reason := strings.TrimSpace(err.Error())

	e.mu.Lock()
	wasActive := now.Before(e.llmUnavailableUntil)
	until := now.Add(scannerLLMCooldown)
	if until.After(e.llmUnavailableUntil) {
		e.llmUnavailableUntil = until
		e.llmUnavailableReason = reason
		e.lastCooldownNoticeAt = now
	}
	retryAt := e.llmUnavailableUntil
	e.mu.Unlock()

	if !wasActive {
		e.log.Warn("scanner entered LLM backend cooldown",
			"retry_at", retryAt,
			"cooldown", scannerLLMCooldown,
			"reason", reason,
		)
	}
}

func (e *Engine) clearLLMCooldown() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.llmUnavailableUntil = time.Time{}
	e.llmUnavailableReason = ""
	e.lastCooldownNoticeAt = time.Time{}
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
	if sig.NarrativeClusterID != "" {
		sb.WriteString(fmt.Sprintf("Narrative: %s\n", sig.NarrativeClusterID))
	}
	if len(sig.Languages) > 0 {
		sb.WriteString(fmt.Sprintf("Original language: %s\n", strings.ToLower(sig.Languages[0])))
	}
	if sig.TranslationProvider != "" || sig.TranslationConfidence > 0 {
		sb.WriteString(fmt.Sprintf("Translation: provider=%s confidence=%.2f\n", sig.TranslationProvider, sig.TranslationConfidence))
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
	if len(sig.CorroboratingLanguages) > 0 {
		sb.WriteString(fmt.Sprintf("Corroborating languages: %s\n", strings.Join(sampleStrings(sig.CorroboratingLanguages, relatedLimit), ", ")))
	}
	if sig.EvidenceMeta != nil {
		sb.WriteString(fmt.Sprintf("Source trust: %.2f\n", sig.EvidenceMeta.SourceTrust))
		if sig.EvidenceMeta.SourceTier != "" || sig.EvidenceMeta.SourceType != "" {
			sb.WriteString(fmt.Sprintf("Source quality: tier=%s type=%s\n", sig.EvidenceMeta.SourceTier, sig.EvidenceMeta.SourceType))
		}
		if sig.EvidenceMeta.SourceDomain != "" || sig.EvidenceMeta.SourceOwnerGroup != "" {
			sb.WriteString(fmt.Sprintf("Source lineage: domain=%s owner_group=%s\n", sig.EvidenceMeta.SourceDomain, sig.EvidenceMeta.SourceOwnerGroup))
		}
		if sig.EvidenceMeta.OriginRegion != "" {
			sb.WriteString(fmt.Sprintf("Origin region: %s\n", sig.EvidenceMeta.OriginRegion))
		}
		if len(sig.EvidenceMeta.CorroboratingOwnerGroups) > 0 {
			sb.WriteString(fmt.Sprintf("Independent owner groups: %s\n", strings.Join(sampleStrings(sig.EvidenceMeta.CorroboratingOwnerGroups, relatedLimit), ", ")))
		}
		if sig.EvidenceMeta.LeadTimeObservations > 0 {
			sb.WriteString(fmt.Sprintf("Historical lead time: avg %.2fh across %d narratives (score %.2f)\n",
				sig.EvidenceMeta.LeadTimeAverageHours,
				sig.EvidenceMeta.LeadTimeObservations,
				sig.EvidenceMeta.LeadTimeScore,
			))
		}
		if sig.EvidenceMeta.DistinctLanguages > 0 {
			sb.WriteString(fmt.Sprintf("Distinct languages: %d\n", sig.EvidenceMeta.DistinctLanguages))
		}
		if sig.EvidenceMeta.FreshnessStatus != "" {
			sb.WriteString(fmt.Sprintf("Freshness: %s (age %.1fh / window %.1fh)\n", sig.EvidenceMeta.FreshnessStatus, sig.EvidenceMeta.FreshnessAgeHours, sig.EvidenceMeta.FreshnessWindowHours))
		}
		if sig.EvidenceMeta.ContradictionCount > 0 {
			sb.WriteString(fmt.Sprintf("Contradictions: %d (%s)\n", sig.EvidenceMeta.ContradictionCount, sig.EvidenceMeta.ContradictionSeverity))
		}
		sb.WriteString(fmt.Sprintf("Evidence score: %.2f\n", sig.EvidenceMeta.EvidenceScore))
		if vector := sig.EvidenceMeta.ConfidenceVector; vector != nil && vector.Present() {
			sb.WriteString(fmt.Sprintf(
				"Confidence vector: fact=%.2f novelty=%.2f market_map=%.2f expression=%.2f execution=%.2f competence=%.2f\n",
				vector.FactConfidence,
				vector.NoveltyConfidence,
				vector.MarketMappingConfidence,
				vector.ExpressionConfidence,
				vector.ExecutionConfidence,
				vector.CompetenceConfidence,
			))
		}
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

func buildCompactPrompt(domain string, sig signal.Signal) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Domain: %s\n", domain))
	sb.WriteString(fmt.Sprintf("Source: %s\n", sig.Source))
	sb.WriteString(fmt.Sprintf("Type: %s\n", sig.Type))
	sb.WriteString(fmt.Sprintf("Urgency: %.2f\n", sig.Urgency))
	if len(sig.CorroboratingSources) > 0 {
		sb.WriteString(fmt.Sprintf("Corroborating sources: %s\n", strings.Join(sampleStrings(sig.CorroboratingSources, 2), ", ")))
	}
	if sig.EvidenceMeta != nil {
		sb.WriteString(fmt.Sprintf("Source trust: %.2f\n", sig.EvidenceMeta.SourceTrust))
		sb.WriteString(fmt.Sprintf("Evidence score: %.2f\n", sig.EvidenceMeta.EvidenceScore))
	}
	if len(sig.Entities) > 0 {
		names := make([]string, 0, 4)
		for _, entity := range sig.Entities {
			if entity.Name == "" {
				continue
			}
			names = append(names, entity.Name)
			if len(names) == 4 {
				break
			}
		}
		if len(names) > 0 {
			sb.WriteString(fmt.Sprintf("Entities: %s\n", strings.Join(names, ", ")))
		}
	}
	content := sig.Translated
	if content == "" && len(sig.Raw) > 0 {
		content = string(sig.Raw)
	}
	sb.WriteString(fmt.Sprintf("Content: %s\n", truncateForPrompt(content, 180)))
	return sb.String()
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

func isUnavailableLLMError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "dial tcp") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "no such host") ||
		strings.Contains(message, "temporary failure in name resolution") ||
		strings.Contains(message, "server misbehaving") ||
		strings.Contains(message, "status 429") ||
		strings.Contains(message, "status 500") ||
		strings.Contains(message, "status 502") ||
		strings.Contains(message, "status 503") ||
		strings.Contains(message, "status 504")
}
