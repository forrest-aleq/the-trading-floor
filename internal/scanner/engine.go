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
	"github.com/hnic/trading-floor/internal/execution/kalshi"
	"github.com/hnic/trading-floor/internal/institutional"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
	"golang.org/x/sync/singleflight"
)

type evaluationTimeContextKey struct{}

// Engine evaluates signals for tradeable opportunities using the speed-tier LLM
type Engine struct {
	log                  *slog.Logger
	llm                  *llm.Router
	minScore             float64 // Minimum score to pass (0-100)
	selectedModel        string
	fallbackModels       []string
	responseMode         scannerResponseMode
	compilerModel        string
	structuredPrompt     string
	structuredFastPrompt string
	thoughtPrompt        string
	compilerPrompt       string

	mu                   sync.Mutex
	llmUnavailableUntil  time.Time
	llmUnavailableReason string
	lastCooldownNoticeAt time.Time

	cacheMu    sync.RWMutex
	cache      map[string]cachedEvaluation
	cacheTTL   time.Duration
	cacheGroup singleflight.Group
}

type cachedEvaluation struct {
	evaluation Evaluation
	cachedAt   time.Time
	ttl        time.Duration
}

type Evaluation struct {
	Opportunity *model.Opportunity
	Accepted    bool
	Reason      string
	Score       float64
	Tradeable   bool
}

var (
	scannerRequestTimeout               = readDurationEnv("SCANNER_REQUEST_TIMEOUT", 15*time.Second)
	scannerMaxTokens                    = readIntEnv("SCANNER_MAX_TOKENS", 128)
	scannerCompactMaxTokens             = readIntEnv("SCANNER_COMPACT_MAX_TOKENS", 96)
	scannerThinkingRequestTimeout       = readDurationEnv("SCANNER_THINKING_REQUEST_TIMEOUT", 12*time.Second)
	scannerThinkingMaxTokens            = readIntEnv("SCANNER_THINKING_MAX_TOKENS", 128)
	scannerThinkingCompactTokens        = readIntEnv("SCANNER_THINKING_COMPACT_MAX_TOKENS", 96)
	scannerCompilerTimeout              = readDurationEnv("SCANNER_COMPILER_TIMEOUT", 15*time.Second)
	scannerCompilerMaxTokens            = readIntEnv("SCANNER_COMPILER_MAX_TOKENS", 128)
	scannerContentLimit                 = readIntEnv("SCANNER_CONTENT_LIMIT", 500)
	scannerCompactContentMax            = readIntEnv("SCANNER_COMPACT_CONTENT_LIMIT", 220)
	scannerStaleSignalAge               = readDurationEnv("SCANNER_STALE_SIGNAL_AGE", 6*time.Hour)
	scannerLLMCooldown                  = readDurationEnv("SCANNER_LLM_UNAVAILABLE_COOLDOWN", 20*time.Second)
	scannerEvalCacheTTL                 = readDurationEnv("SCANNER_EVAL_CACHE_TTL", 10*time.Minute)
	scannerErrorCacheTTL                = readDurationEnv("SCANNER_ERROR_CACHE_TTL", 30*time.Second)
	kalshiMarketDiscoveryEnabled        = readBoolEnv("KALSHI_MARKET_DISCOVERY_ENABLED", false)
	kalshiMarketDiscoveryScore          = readFloatEnv("KALSHI_MARKET_DISCOVERY_SCORE", 58)
	kalshiMarketDiscoveryMaxSpread      = readFloatEnv("KALSHI_MARKET_DISCOVERY_MAX_SPREAD", 0.20)
	kalshiMarketDiscoveryBuyCheaperSide = readBoolEnv("KALSHI_MARKET_DISCOVERY_BUY_CHEAPER_SIDE", true)
)

func ReloadRuntimeConfig() {
	scannerRequestTimeout = readDurationEnv("SCANNER_REQUEST_TIMEOUT", 15*time.Second)
	scannerMaxTokens = readIntEnv("SCANNER_MAX_TOKENS", 128)
	scannerCompactMaxTokens = readIntEnv("SCANNER_COMPACT_MAX_TOKENS", 96)
	scannerThinkingRequestTimeout = readDurationEnv("SCANNER_THINKING_REQUEST_TIMEOUT", 12*time.Second)
	scannerThinkingMaxTokens = readIntEnv("SCANNER_THINKING_MAX_TOKENS", 128)
	scannerThinkingCompactTokens = readIntEnv("SCANNER_THINKING_COMPACT_MAX_TOKENS", 96)
	scannerCompilerTimeout = readDurationEnv("SCANNER_COMPILER_TIMEOUT", 15*time.Second)
	scannerCompilerMaxTokens = readIntEnv("SCANNER_COMPILER_MAX_TOKENS", 128)
	scannerContentLimit = readIntEnv("SCANNER_CONTENT_LIMIT", 500)
	scannerCompactContentMax = readIntEnv("SCANNER_COMPACT_CONTENT_LIMIT", 220)
	scannerStaleSignalAge = readDurationEnv("SCANNER_STALE_SIGNAL_AGE", 6*time.Hour)
	scannerLLMCooldown = readDurationEnv("SCANNER_LLM_UNAVAILABLE_COOLDOWN", 20*time.Second)
	scannerEvalCacheTTL = readDurationEnv("SCANNER_EVAL_CACHE_TTL", 10*time.Minute)
	scannerErrorCacheTTL = readDurationEnv("SCANNER_ERROR_CACHE_TTL", 30*time.Second)
	kalshiMarketDiscoveryEnabled = readBoolEnv("KALSHI_MARKET_DISCOVERY_ENABLED", false)
	kalshiMarketDiscoveryScore = readFloatEnv("KALSHI_MARKET_DISCOVERY_SCORE", 58)
	kalshiMarketDiscoveryMaxSpread = readFloatEnv("KALSHI_MARKET_DISCOVERY_MAX_SPREAD", 0.20)
	kalshiMarketDiscoveryBuyCheaperSide = readBoolEnv("KALSHI_MARKET_DISCOVERY_BUY_CHEAPER_SIDE", true)
}

func ApplyPaperDiscoveryDefaults() {
	if strings.TrimSpace(os.Getenv("KALSHI_MARKET_DISCOVERY_ENABLED")) == "" {
		kalshiMarketDiscoveryEnabled = true
	}
	if strings.TrimSpace(os.Getenv("KALSHI_MARKET_DISCOVERY_SCORE")) == "" {
		kalshiMarketDiscoveryScore = 52
	}
	if strings.TrimSpace(os.Getenv("KALSHI_MARKET_DISCOVERY_MAX_SPREAD")) == "" {
		kalshiMarketDiscoveryMaxSpread = 0.20
	}
	if strings.TrimSpace(os.Getenv("KALSHI_MARKET_DISCOVERY_BUY_CHEAPER_SIDE")) == "" {
		kalshiMarketDiscoveryBuyCheaperSide = true
	}
}

func NewEngine(llmRouter *llm.Router, minScore float64) *Engine {
	if minScore == 0 {
		minScore = 70 // Default: aggressive filter — most signals should be rejected
	}
	selectedModel := scannerSelectedModel()
	policy := activePromptPolicy()
	return &Engine{
		log:                  slog.Default().With("component", "scanner"),
		llm:                  llmRouter,
		minScore:             minScore,
		selectedModel:        selectedModel,
		fallbackModels:       scannerFallbackModels(selectedModel),
		responseMode:         detectScannerResponseMode(selectedModel),
		compilerModel:        strings.TrimSpace(os.Getenv("SCANNER_COMPILER_MODEL")),
		structuredPrompt:     policy.structuredPrompt,
		structuredFastPrompt: policy.structuredFastPrompt,
		thoughtPrompt:        policy.thoughtPrompt,
		compilerPrompt:       policy.compilerPrompt,
		cache:                make(map[string]cachedEvaluation),
		cacheTTL:             scannerEvalCacheTTL,
	}
}

type scannerResponseMode string

const (
	scannerResponseModeStructured scannerResponseMode = "structured_json"
	scannerResponseModeThought    scannerResponseMode = "thought_block"
)

// Evaluate checks if a signal contains a tradeable opportunity
func (e *Engine) Evaluate(ctx context.Context, sig signal.Signal, domain string) (*model.Opportunity, bool) {
	result := e.EvaluateDetailed(ctx, sig, domain)
	return result.Opportunity, result.Accepted
}

// EvaluateDetailed runs the scanner and records deterministic rejection reasons for replay and regression analysis.
func (e *Engine) EvaluateDetailed(ctx context.Context, sig signal.Signal, domain string) Evaluation {
	cacheKey := e.evaluationCacheKey(sig, domain)
	if cached, ok := e.lookupCachedEvaluation(cacheKey, time.Now().UTC()); ok {
		return cloneEvaluation(cached)
	}
	if cacheKey != "" {
		cachedValue, _, _ := e.cacheGroup.Do(cacheKey, func() (any, error) {
			if cached, ok := e.lookupCachedEvaluation(cacheKey, time.Now().UTC()); ok {
				return cached, nil
			}
			evaluation := e.evaluateDetailedUncached(ctx, sig, domain)
			e.storeCachedEvaluation(cacheKey, evaluation, time.Now().UTC())
			return evaluation, nil
		})
		if evaluation, ok := cachedValue.(Evaluation); ok {
			return cloneEvaluation(evaluation)
		}
	}
	return e.evaluateDetailedUncached(ctx, sig, domain)
}

func (e *Engine) evaluateDetailedUncached(ctx context.Context, sig signal.Signal, domain string) Evaluation {
	if skip, reason := shouldSkipSignalAt(sig, evaluationTime(ctx)); skip {
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
		return Evaluation{Reason: "prefilter:" + reason}
	}

	if evaluation, ok := e.evaluateKalshiMarketDiscovery(sig, domain); ok {
		return evaluation
	}

	if until, reason, shouldLog := e.llmCooldown(time.Now().UTC()); !until.IsZero() {
		if shouldLog {
			e.log.Warn("scanner skipping LLM requests during backend cooldown",
				"retry_at", until,
				"reason", reason,
			)
		}
		return Evaluation{Reason: "llm_cooldown"}
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
		if isScannerTimeoutError(err) {
			if fallbackResp, fallbackModel, fallbackErr := e.retryFastModelFallback(ctx, requestCfg.systemPrompt, candidate.content, candidate.maxTokens, 0.0, requestCfg.jsonMode); fallbackErr == nil {
				e.log.Info("scanner fast-model fallback recovered decision",
					"signal_id", sig.ID,
					"fallback_model", fallbackModel,
					"prompt_chars", len(candidate.content),
					"max_tokens", candidate.maxTokens,
				)
				resp = fallbackResp
				usedPrompt = candidate.content
				e.clearLLMCooldown()
				break
			}
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
			return Evaluation{Reason: "llm_error"}
		}
		e.log.Warn("scanner request exceeded primary scan budget, retrying compact prompt",
			"signal_id", sig.ID,
			"attempt", candidate.name,
			"prompt_chars", len(candidate.content),
			"max_tokens", candidate.maxTokens,
		)
	}

	result, err := parseScanResponse(resp)
	if err != nil && requestCfg.allowStructuredFallback {
		if fallbackResp, fallbackPrompt, fallbackErr := e.retryStructuredFallback(ctx, domain, sig); fallbackErr == nil {
			if parsed, parseErr := parseScanResponse(fallbackResp); parseErr == nil {
				e.log.Info("scanner structured fallback recovered parse failure",
					"signal_id", sig.ID,
					"prompt_chars", len(fallbackPrompt),
					"max_tokens", scannerCompactMaxTokens,
				)
				result = parsed
				err = nil
				resp = fallbackResp
			}
		}
	}
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
		if recovered, ok := recoverConservativeThoughtReject(resp, true); ok {
			e.log.Info("scanner defaulted malformed thought trace to reject",
				"signal_id", sig.ID,
				"reason", recovered.Reasoning,
			)
			result = recovered
			err = nil
		}
	}
	if err != nil {
		e.log.Warn("scanner parse error",
			"error", err,
			"signal_id", sig.ID,
			"response_len", len(resp),
			"response_excerpt", institutional.TruncateForPrompt(resp, 320),
		)
		return Evaluation{Reason: "parse_error"}
	}

	if !result.Tradeable {
		return Evaluation{Reason: "scanner_rejected", Score: result.Score, Tradeable: false}
	}
	if result.Score < e.minScore {
		return Evaluation{Reason: "score_below_threshold", Score: result.Score, Tradeable: true}
	}

	instruments := make([]model.Instrument, 0, len(result.Instruments))
	for _, inst := range result.Instruments {
		instrument, ok := normalizeScannerInstrument(inst, domain)
		if !ok {
			continue
		}
		instruments = append(instruments, instrument)
	}
	if len(instruments) == 0 {
		return Evaluation{Reason: "no_instruments", Score: result.Score, Tradeable: true}
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

	return Evaluation{
		Opportunity: opp,
		Accepted:    true,
		Reason:      "accepted",
		Score:       result.Score,
		Tradeable:   true,
	}
}

func (e *Engine) evaluationCacheKey(sig signal.Signal, domain string) string {
	if strings.TrimSpace(domain) == "" || strings.TrimSpace(sig.ID) == "" {
		return ""
	}
	keyParts := []string{
		strings.TrimSpace(domain),
		strings.TrimSpace(sig.ID),
		strings.TrimSpace(sig.ContentHash),
		strings.TrimSpace(sig.InstitutionalContext),
	}
	if sig.Expectation != nil {
		keyParts = append(keyParts,
			sig.Expectation.PredictedAction,
			fmt.Sprintf("%.3f", sig.Expectation.PredictedImportance),
			fmt.Sprintf("%.3f", sig.Expectation.PredictedReliability),
			fmt.Sprintf("%.3f", sig.Expectation.PredictedTradability),
		)
	}
	if sig.Appraisal != nil {
		keyParts = append(keyParts,
			sig.Appraisal.ViolationClass,
			fmt.Sprintf("%.3f", sig.Appraisal.ActionPressure),
			fmt.Sprintf("%.3f", sig.Appraisal.SocialCost),
		)
	}
	if sig.ActionSelection != nil {
		keyParts = append(keyParts,
			sig.ActionSelection.RecommendedAction,
			fmt.Sprintf("%.3f", sig.ActionSelection.ExpectedUtility),
		)
	}
	return strings.Join(keyParts, "|")
}

func (e *Engine) lookupCachedEvaluation(key string, now time.Time) (Evaluation, bool) {
	if key == "" {
		return Evaluation{}, false
	}
	e.cacheMu.RLock()
	cached, ok := e.cache[key]
	e.cacheMu.RUnlock()
	if !ok {
		return Evaluation{}, false
	}
	ttl := cached.ttl
	if ttl <= 0 {
		ttl = e.cacheTTL
	}
	if ttl <= 0 {
		return Evaluation{}, false
	}
	if now.Sub(cached.cachedAt) > ttl {
		e.cacheMu.Lock()
		delete(e.cache, key)
		e.cacheMu.Unlock()
		return Evaluation{}, false
	}
	return cached.evaluation, true
}

func (e *Engine) storeCachedEvaluation(key string, evaluation Evaluation, now time.Time) {
	if key == "" {
		return
	}
	ttl := e.cacheTTL
	switch strings.TrimSpace(evaluation.Reason) {
	case "llm_error", "parse_error":
		ttl = scannerErrorCacheTTL
	}
	if ttl <= 0 {
		return
	}
	e.cacheMu.Lock()
	e.cache[key] = cachedEvaluation{evaluation: evaluation, cachedAt: now, ttl: ttl}
	e.cacheMu.Unlock()
}

func cloneEvaluation(in Evaluation) Evaluation {
	out := in
	if in.Opportunity != nil {
		cloned := *in.Opportunity
		cloned.SignalIDs = append([]string(nil), in.Opportunity.SignalIDs...)
		cloned.Instruments = append([]model.Instrument(nil), in.Opportunity.Instruments...)
		if in.Opportunity.EvidenceMeta != nil {
			cloned.EvidenceMeta = in.Opportunity.EvidenceMeta.Clone()
		}
		cloned.ID = uuid.New().String()
		cloned.CreatedAt = time.Now().UTC()
		out.Opportunity = &cloned
	}
	return out
}

type kalshiMarketDiscoverySnapshot struct {
	Ticker           string `json:"ticker"`
	Title            string `json:"title"`
	Subtitle         string `json:"subtitle"`
	Status           string `json:"status"`
	YesBidDollars    string `json:"yes_bid_dollars"`
	YesAskDollars    string `json:"yes_ask_dollars"`
	NoBidDollars     string `json:"no_bid_dollars"`
	NoAskDollars     string `json:"no_ask_dollars"`
	LastPriceDollars string `json:"last_price_dollars"`
	CloseTime        string `json:"close_time"`
	ExpirationTime   string `json:"expiration_time"`
}

func (e *Engine) evaluateKalshiMarketDiscovery(sig signal.Signal, domain string) (Evaluation, bool) {
	if !kalshiMarketDiscoveryEnabled ||
		!strings.EqualFold(strings.TrimSpace(domain), "prediction_market") ||
		!strings.EqualFold(strings.TrimSpace(sig.Source), "kalshi-market") {
		return Evaluation{}, false
	}

	market, ok := decodeKalshiMarketDiscoverySnapshot(sig)
	if !ok {
		return Evaluation{Reason: "kalshi_market_unreadable", Tradeable: true}, true
	}
	ticker := strings.ToUpper(strings.TrimSpace(market.Ticker))
	if !model.IsKalshiTicker(ticker) {
		return Evaluation{Reason: "no_instruments", Tradeable: true}, true
	}
	if kalshi.ShouldBlockMultivariateTicker(ticker) {
		return Evaluation{Reason: "kalshi_mve_wrapper_blocked", Tradeable: true}, true
	}
	if !kalshiMarketDiscoveryStatusTradable(market.Status) {
		return Evaluation{Reason: "kalshi_market_not_open", Tradeable: true}, true
	}

	direction := model.Long
	entryPrice, priceOK := kalshiDiscoveryPrice(market.YesAskDollars, market.LastPriceDollars, market.YesBidDollars)
	if kalshiMarketDiscoveryBuyCheaperSide {
		if noPrice, noOK := kalshiDiscoveryPrice(market.NoAskDollars, "", market.NoBidDollars); noOK && (!priceOK || noPrice < entryPrice) {
			direction = model.Short
			entryPrice = noPrice
			priceOK = true
		}
	}
	if !priceOK {
		return Evaluation{Reason: "kalshi_market_missing_price", Tradeable: true}, true
	}

	spread, spreadOK := kalshiDiscoverySpread(market, direction)
	if spreadOK && kalshiMarketDiscoveryMaxSpread > 0 && spread > kalshiMarketDiscoveryMaxSpread {
		return Evaluation{Reason: "kalshi_market_spread_too_wide", Score: kalshiDiscoveryScore(sig, spread), Tradeable: true}, true
	}

	score := kalshiDiscoveryScore(sig, spread)
	if score < e.minScore {
		return Evaluation{Reason: "score_below_threshold", Score: score, Tradeable: true}, true
	}

	inst := model.NormalizeKalshiInstrument(model.Instrument{
		Symbol:   ticker,
		SecType:  model.SecTypeKalshi,
		Currency: "USD",
	})
	opp := &model.Opportunity{
		ID:           uuid.New().String(),
		SignalIDs:    []string{sig.ID},
		Instruments:  []model.Instrument{inst},
		Direction:    direction,
		Urgency:      sig.Urgency,
		Score:        score,
		Category:     "prediction_market",
		EvidenceMeta: sig.EvidenceMeta.Clone(),
		CreatedAt:    time.Now().UTC(),
	}
	e.log.Info("kalshi market discovery opportunity",
		"signal_id", sig.ID,
		"ticker", ticker,
		"direction", direction,
		"entry_price", entryPrice,
		"spread", spread,
		"score", score,
		"status", market.Status,
		"title", institutional.TruncateForPrompt(firstNonEmptyScanner(market.Title, market.Subtitle), 120),
	)
	return Evaluation{
		Opportunity: opp,
		Accepted:    true,
		Reason:      "kalshi_market_discovery",
		Score:       score,
		Tradeable:   true,
	}, true
}

func decodeKalshiMarketDiscoverySnapshot(sig signal.Signal) (kalshiMarketDiscoverySnapshot, bool) {
	var market kalshiMarketDiscoverySnapshot
	if len(sig.Raw) > 0 {
		if err := json.Unmarshal(sig.Raw, &market); err == nil && strings.TrimSpace(market.Ticker) != "" {
			return market, true
		}
	}
	for _, entity := range sig.Entities {
		if model.IsKalshiTicker(entity.ID) {
			market.Ticker = entity.ID
			return market, true
		}
		if model.IsKalshiTicker(entity.Name) {
			market.Ticker = entity.Name
			return market, true
		}
	}
	return market, false
}

func kalshiDiscoveryPrice(primary, secondary, tertiary string) (float64, bool) {
	for _, raw := range []string{primary, secondary, tertiary} {
		price, ok := parseScannerProbability(raw)
		if ok {
			return price, true
		}
	}
	return 0, false
}

func kalshiMarketDiscoveryStatusTradable(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active", "open":
		return true
	default:
		return false
	}
}

func kalshiDiscoverySpread(market kalshiMarketDiscoverySnapshot, direction model.TradeDirection) (float64, bool) {
	bidRaw := market.YesBidDollars
	askRaw := market.YesAskDollars
	if direction == model.Short {
		bidRaw = market.NoBidDollars
		askRaw = market.NoAskDollars
	}
	bid, bidOK := parseScannerProbability(bidRaw)
	ask, askOK := parseScannerProbability(askRaw)
	if bidOK && askOK && ask > bid {
		return ask - bid, true
	}
	return 0, false
}

func kalshiDiscoveryScore(sig signal.Signal, spread float64) float64 {
	score := kalshiMarketDiscoveryScore
	if sig.Urgency >= 0.6 {
		score += 5
	}
	if spread > 0 && spread <= 0.05 {
		score += 4
	}
	if score > 100 {
		return 100
	}
	if score < 0 {
		return 0
	}
	return score
}

func parseScannerProbability(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value < 0.01 || value > 0.99 {
		return 0, false
	}
	return value, true
}

func firstNonEmptyScanner(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func WithEvaluationTime(ctx context.Context, at time.Time) context.Context {
	if at.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, evaluationTimeContextKey{}, at)
}

func evaluationTime(ctx context.Context) time.Time {
	if ctx != nil {
		if at, ok := ctx.Value(evaluationTimeContextKey{}).(time.Time); ok && !at.IsZero() {
			return at
		}
	}
	return time.Now().UTC()
}

func (e *Engine) retryStructuredFallback(ctx context.Context, domain string, sig signal.Signal) (string, string, error) {
	prompt := buildCompactPrompt(domain, sig)
	reqCtx, cancel := context.WithTimeout(ctx, scannerRequestTimeout)
	defer cancel()

	resp, err := e.askScannerWithLimit(reqCtx, e.structuredFastPrompt, prompt, scannerCompactMaxTokens, 0.0, true)
	if err != nil {
		return "", prompt, err
	}
	return resp, prompt, nil
}

func (e *Engine) askScannerWithLimit(ctx context.Context, system, prompt string, maxTokens int, temperature float64, jsonMode bool) (string, error) {
	return e.askScannerModelWithLimit(ctx, e.selectedModel, system, prompt, maxTokens, temperature, jsonMode)
}

func (e *Engine) askScannerModelWithLimit(ctx context.Context, model, system, prompt string, maxTokens int, temperature float64, jsonMode bool) (string, error) {
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: prompt},
		},
		Model:       model,
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

func (e *Engine) retryFastModelFallback(ctx context.Context, system, prompt string, maxTokens int, temperature float64, jsonMode bool) (string, string, error) {
	for _, model := range e.fallbackModels {
		reqCtx, cancel := context.WithTimeout(ctx, scannerRequestTimeout)
		resp, err := e.askScannerModelWithLimit(reqCtx, model, system, prompt, maxTokens, temperature, jsonMode)
		cancel()
		if err == nil {
			return resp, model, nil
		}
	}
	return "", "", fmt.Errorf("no fast-model fallback succeeded")
}

func (e *Engine) compileScannerDecision(ctx context.Context, signalPrompt, rawResponse string) (string, error) {
	if e.compilerModel == "" {
		return "", fmt.Errorf("scanner compiler model not configured")
	}

	compileCtx, cancel := context.WithTimeout(ctx, scannerCompilerTimeout)
	defer cancel()

	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: e.compilerPrompt},
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
			systemPrompt:            addTerminalDecisionContract(e.thoughtPrompt),
			jsonMode:                false,
			timeout:                 scannerThinkingRequestTimeout,
			maxTokens:               scannerThinkingMaxTokens,
			compactMaxTokens:        scannerThinkingCompactTokens,
			allowCompilerFallback:   e.compilerModel != "",
			allowStructuredFallback: true,
		}
	}

	return scannerRequestConfig{
		systemPrompt:            e.structuredPrompt,
		jsonMode:                true,
		timeout:                 scannerRequestTimeout,
		maxTokens:               scannerMaxTokens,
		compactMaxTokens:        scannerCompactMaxTokens,
		allowCompilerFallback:   e.compilerModel != "",
		allowStructuredFallback: true,
	}
}

func scannerSelectedModel() string {
	if model := strings.TrimSpace(os.Getenv("SCANNER_MODEL")); model != "" {
		return model
	}
	if model := strings.TrimSpace(os.Getenv("LLM_MODEL_SPEED")); model != "" {
		return model
	}
	return llm.DefaultCloudSpeedModel
}

func scannerFallbackModels(selectedModel string) []string {
	seen := map[string]struct{}{}
	models := make([]string, 0, 3)
	selected := strings.TrimSpace(selectedModel)
	if selected != "" {
		seen[strings.ToLower(selected)] = struct{}{}
	}
	for _, raw := range strings.Split(strings.TrimSpace(os.Getenv("SCANNER_FALLBACK_MODELS")), ",") {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}
	return models
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
	if isLocalThoughtScannerModel(model) {
		return scannerResponseModeStructured
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

func isLocalThoughtScannerModel(model string) bool {
	baseURL := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_BASE_URL")))
	return isThoughtFriendlyScannerModel(model) &&
		(strings.Contains(baseURL, "127.0.0.1") ||
			strings.Contains(baseURL, "localhost") ||
			strings.Contains(baseURL, "::1"))
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

func addTerminalDecisionContract(systemPrompt string) string {
	return strings.TrimSpace(systemPrompt) + `

You MUST end with exactly one terminal decision block:
FINAL_DECISION
tradeable: true|false
score: 0-100
instruments: SYMBOL:SECTYPE:CURRENCY, ...
direction: long|short|none
urgency: 0.0-1.0
category: macro|corporate|geopolitical|flows|tail|volatility|sector|systematic|prediction_market
reasoning: short explanation
END_FINAL_DECISION

Do not omit the terminal decision block.`
}

func normalizeScannerInstrument(inst struct {
	Symbol   string `json:"symbol"`
	SecType  string `json:"sec_type"`
	Currency string `json:"currency"`
}, domain string) (model.Instrument, bool) {
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if symbol == "" || symbol == "NONE" || symbol == "UNKNOWN" {
		return model.Instrument{}, false
	}

	secType := strings.ToUpper(strings.TrimSpace(inst.SecType))
	currency := strings.ToUpper(strings.TrimSpace(inst.Currency))
	if currency == "" {
		currency = "USD"
	}

	isPredictionMarket := strings.EqualFold(strings.TrimSpace(domain), "prediction_market")
	isExplicitKalshi := strings.EqualFold(secType, model.SecTypeKalshi)
	isKalshiTicker := model.IsKalshiTicker(symbol)
	if isKalshiTicker && !isPredictionMarket {
		return model.Instrument{}, false
	}
	if isExplicitKalshi && !isKalshiTicker {
		return model.Instrument{}, false
	}

	instrument := model.Instrument{
		Symbol:   symbol,
		SecType:  secType,
		Currency: currency,
		Exchange: "SMART",
	}
	if isKalshiTicker && isPredictionMarket {
		return model.NormalizeKalshiInstrument(instrument), true
	}
	if isExplicitKalshi {
		return model.Instrument{}, false
	}
	if secType == "" {
		instrument.SecType = "STK"
	}
	return instrument, true
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
		if recovered, ok := recoverConservativeThoughtReject(raw, false); ok {
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

func recoverConservativeThoughtReject(raw string, allowGeneric bool) (scanResult, bool) {
	normalized := strings.ToLower(strings.ReplaceAll(raw, "\r", ""))
	if !looksLikeScannerThoughtTrace(normalized) {
		return scanResult{}, false
	}

	if isPositiveThoughtTrace(normalized) {
		return scanResult{}, false
	}

	reason := inferThoughtRejectReason(raw)
	if reason == "incomplete thought trace defaulted to reject" && !allowGeneric {
		return scanResult{}, false
	}
	if reason == "incomplete thought trace defaulted to reject" {
		reason = "noncompliant scanner thought trace defaulted to reject"
	}

	return scanResult{
		Tradeable: false,
		Score:     0,
		Direction: "none",
		Urgency:   0,
		Category:  inferThoughtCategory(normalized),
		Reasoning: reason,
	}, true
}

func looksLikeScannerThoughtTrace(normalized string) bool {
	return strings.Contains(normalized, "<think>") ||
		strings.Contains(normalized, "thinking process") ||
		strings.Contains(normalized, "analyze the request") ||
		strings.Contains(normalized, "analyze the signal") ||
		strings.Contains(normalized, "the user wants") ||
		strings.Contains(normalized, "let's break this down") ||
		strings.Contains(normalized, "let me try to figure")
}

func isPositiveThoughtTrace(normalized string) bool {
	return containsAny(normalized,
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
	)
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
			return institutional.TruncateForPrompt(cleaned, 96)
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

func shouldSkipSignalAt(sig signal.Signal, now time.Time) (bool, string) {
	if allowed, reason := sig.EvidenceGate(); !allowed {
		return true, reason
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !sig.Timestamp.IsZero() && now.Sub(sig.Timestamp) > scannerStaleSignalAge {
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

func readFloatEnv(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func readBoolEnv(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
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
	case "prediction_market":
		return `Focus: prediction-market mispricings, election/event odds, Kalshi/Polymarket-style contracts, and event probabilities that imply tradeable cross-asset dislocations.
Look for: probability moves that disagree with liquid-market pricing, stale odds after primary-source news, or event markets leading equities/rates/FX/crypto.
Preferred instruments: Kalshi event tickers when the source signal contains a KX... ticker. Use sec_type "KALSHI", currency "USD", direction "long" to buy YES, direction "short" to buy NO, and entry_price as the dollar probability you are willing to pay for the intended YES/NO outcome.`
	default:
		return ""
	}
}

func formatSignal(sig signal.Signal) string {
	return formatSignalWithLimit(sig, scannerContentLimit, 4, 12)
}

func formatSignalWithLimit(sig signal.Signal, contentLimit, relatedLimit, entityLimit int) string {
	return institutional.BuildSignalContext(sig, institutional.SignalContextOptions{
		ContentLimit:         contentLimit,
		RelatedLimit:         relatedLimit,
		EntityLimit:          entityLimit,
		IncludeEvidence:      true,
		IncludeInstitutional: true,
	})
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
	_, _ = fmt.Fprintf(&sb, "Domain: %s\n", domain)
	sb.WriteString(institutional.BuildSignalContext(sig, institutional.SignalContextOptions{
		Compact:              true,
		ContentLimit:         180,
		RelatedLimit:         2,
		EntityLimit:          4,
		IncludeEvidence:      true,
		IncludeInstitutional: true,
	}))
	sb.WriteByte('\n')
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
