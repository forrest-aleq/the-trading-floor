package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

// Council convenes multiple strategy archetypes to debate large positions (>2% of portfolio).
// Each archetype evaluates independently, then the council synthesizes.
type Council struct {
	log           *slog.Logger
	llm           *llm.Router
	archetypes    []Archetype
	telemetry     VoiceTelemetryProvider
	selectedModel string
	responseMode  structuredResponseMode
	compilerModel string
}

const (
	councilPerspectiveMaxTokens = 384
	councilPerspectiveTimeout   = 25 * time.Second
	councilCompilerTimeout      = 15 * time.Second
	councilCompilerMaxTokens    = 600
)

const councilThoughtPrefix = `Do not restate the request or schema.
Think if useful, but keep it concise.
You must end with exactly one JSON object matching the requested schema.`

const councilCompilerPrompt = `You are a council-perspective compiler.
You will receive the original council task and a freeform reasoning transcript from one council voice.
Return one final JSON object only. No prose, no markdown, no thinking.

JSON schema:
{"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": 0.0, "size_adjustment": 1.0, "reasoning": "..."}`

type perspectiveResult struct {
	name           string
	view           string
	reasoning      string
	recommendation model.CouncilRecommendation
	conviction     float64
	size           float64
	weight         float64
	accuracy       float64
	observations   int
}

// Archetype represents a strategic perspective for council debate.
type Archetype struct {
	Name   string // e.g. "Fundamental", "Contrarian", "Macro", "Tail", "Scalper"
	Prompt string // System prompt defining this archetype's perspective
}

type VoiceTelemetryProvider interface {
	CouncilVoiceTelemetry(ctx context.Context, domain string) (map[string]model.CouncilVoiceStats, error)
}

func NewCouncil(llmRouter *llm.Router) *Council {
	selectedModel := criticalSelectedModel()
	return &Council{
		log:           slog.Default().With("component", "council"),
		llm:           llmRouter,
		selectedModel: selectedModel,
		responseMode:  detectStructuredResponseMode(os.Getenv("COUNCIL_RESPONSE_MODE"), selectedModel),
		compilerModel: structuredCompilerModel("COUNCIL_COMPILER_MODEL"),
		archetypes: []Archetype{
			{
				Name: "Fundamental",
				Prompt: `You are a fundamental analyst on the trading council. Evaluate this thesis purely on numbers and fundamentals.
Ask: Do the financials support this? What are the valuation multiples? Is growth priced in? What do margins look like?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Contrarian",
				Prompt: `You are the contrarian voice on the trading council. Your job is to check if this trade is already crowded.
Ask: Is everyone already positioned this way? Is this the obvious trade? What happens if the crowd reverses? Where is the pain trade?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Macro",
				Prompt: `You are the macro strategist on the trading council. Evaluate whether the macro regime supports this thesis.
Ask: Does the rate environment help or hurt? What is the vol regime? Is risk appetite expanding or contracting? Does this trade fight the Fed?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Tail",
				Prompt: `You are the tail risk analyst on the trading council. Your job is to find the worst case scenario.
Ask: What kills this trade? What is the max loss? Is there gap risk? What black swan event invalidates the thesis? Is the risk/reward actually asymmetric?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Timing",
				Prompt: `You are the timing/execution specialist on the trading council. Evaluate whether the entry timing is right.
Ask: Is the market trending or mean-reverting? Are we chasing? Is there a better entry? What does the order flow look like? Should we wait for a pullback?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Market-Implied",
				Prompt: `You are the market-implied voice on the trading council. Your job is to ask what is already priced in.
Ask: Does recent price action already reflect the thesis? Does implied move or skew already encode the event? Is the reaction gap actually large enough to trade?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Source-Forensics",
				Prompt: `You are the source-forensics voice on the trading council. Your job is to challenge the evidence integrity.
Ask: Is this primary reporting or copy-derived? Are the sources independent? Is there contradiction, manipulation risk, or weak provenance? Is the signal too stale or too social-noise heavy?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Execution-Microstructure",
				Prompt: `You are the execution and microstructure voice on the trading council. Your job is to challenge whether this expression can actually be entered and exited cleanly.
Ask: Is the structure liquid enough? Does the quant profile show acceptable max loss and margin? Are we likely to suffer slippage or poor fills? Is there a cleaner structure to express the same view?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Abstain",
				Prompt: `You are the abstain voice on the trading council. Your job is to defend the null hypothesis and explain why we should do nothing.
Ask: What is the strongest reason to stay flat? What information is missing? Is there a better waiting point or cleaner setup later? Are we overfitting weak evidence into a trade?
Respond in JSON: {"perspective": "...", "recommendation": "approve|reject|abstain", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
		},
	}
}

func (c *Council) SetVoiceTelemetryProvider(provider VoiceTelemetryProvider) {
	c.telemetry = provider
}

// Debate convenes all archetypes to evaluate a thesis in parallel.
func (c *Council) Debate(ctx context.Context, thesis *model.Thesis) *model.CouncilVerdict {
	telemetry := c.voiceTelemetry(ctx, thesis.Domain)
	thesisPrompt := fmt.Sprintf(`Thesis under council review:

Symbol: %s (%s)
Direction: %s
Strategy: %s
Conviction: %.2f
Entry: %.2f / Target: %.2f / Stop: %.2f
Time Horizon: %s
Position Size (notional %%): %.2f

Evidence: %s
Counter Arguments: %s
Prosecution Verdict: %s
Quant Metrics:
%s`,
		thesis.Instrument.Symbol, thesis.Instrument.SecType,
		thesis.Direction, thesis.Strategy,
		thesis.Conviction,
		thesis.EntryPrice, thesis.TargetPrice, thesis.StopLoss,
		thesis.TimeHorizon,
		thesis.PositionSize,
		formatEvidence(thesis.Evidence),
		formatCounterArgs(thesis.CounterArgs),
		prosecutionVerdict(thesis.Prosecution),
		formatQuantMetrics(thesis.QuantMetrics),
	)

	var mu sync.Mutex
	var results []perspectiveResult
	var wg sync.WaitGroup

	for _, arch := range c.archetypes {
		wg.Add(1)
		go func(a Archetype) {
			defer wg.Done()

			callCtx, cancel := context.WithTimeout(ctx, councilPerspectiveTimeout)
			defer cancel()

			cleaned, err := c.requestPerspectiveJSON(callCtx, a.Name, a.Prompt, thesisPrompt)
			if err != nil {
				c.log.Warn("council archetype failed", "archetype", a.Name, "error", err)
				return
			}

			var pr struct {
				Perspective          string  `json:"perspective"`
				Recommendation       string  `json:"recommendation"`
				ConvictionAdjustment float64 `json:"conviction_adjustment"`
				SizeAdjustment       float64 `json:"size_adjustment"`
				Reasoning            string  `json:"reasoning"`
			}
			if err := json.Unmarshal([]byte(cleaned), &pr); err != nil {
				c.log.Warn("council parse failed",
					"archetype", a.Name,
					"error", err,
					"response_excerpt", truncateForLog(cleaned, 320),
				)
				return
			}

			stats := telemetry[a.Name]
			mu.Lock()
			results = append(results, perspectiveResult{
				name:           a.Name,
				view:           pr.Perspective,
				reasoning:      strings.TrimSpace(pr.Reasoning),
				recommendation: normalizeRecommendation(pr.Recommendation, pr.ConvictionAdjustment, pr.SizeAdjustment),
				conviction:     clampCouncilAdjustment(pr.ConvictionAdjustment),
				size:           normalizeSizeAdjustment(pr.SizeAdjustment),
				weight:         normalizeVoiceWeight(stats.Weight),
				accuracy:       stats.Accuracy,
				observations:   stats.TotalCalls,
			})
			mu.Unlock()
		}(arch)
	}

	wg.Wait()

	return c.synthesize(thesis, results)
}

func (c *Council) requestPerspectiveJSON(ctx context.Context, archetype, systemPrompt, thesisPrompt string) (string, error) {
	resp, err := c.askPerspectiveWithFallbackMode(ctx, systemPrompt, thesisPrompt)
	if err != nil {
		return "", err
	}

	cleaned, extractErr := extractStructuredJSON(resp)
	if extractErr != nil {
		if c.compilerModel != "" {
			if compiled, compileErr := c.compilePerspectiveJSON(ctx, archetype, systemPrompt, thesisPrompt, resp); compileErr == nil {
				if compiledJSON, recoverErr := extractStructuredJSON(compiled); recoverErr == nil {
					c.log.Info("council compiler recovered structured perspective",
						"archetype", archetype,
						"compiler_model", c.compilerModel,
					)
					return compiledJSON, nil
				}
			} else {
				c.log.Warn("council compiler fallback failed",
					"archetype", archetype,
					"compiler_model", c.compilerModel,
					"error", compileErr,
				)
			}
		}
		c.log.Warn("council JSON extraction failed",
			"archetype", archetype,
			"error", extractErr,
			"response_len", len(resp),
			"response_excerpt", truncateForLog(resp, 320),
		)
		return "", extractErr
	}

	return cleaned, nil
}

func (c *Council) askPerspectiveWithFallbackMode(ctx context.Context, systemPrompt, thesisPrompt string) (string, error) {
	if c.responseMode == structuredResponseModeThought {
		systemPrompt = addTerminalJSONContract(councilThoughtPrefix + "\n\n" + systemPrompt)
		return c.llm.AskWithLimit(ctx, llm.TierCritical, systemPrompt, thesisPrompt, councilPerspectiveMaxTokens, 0.2)
	}
	return c.llm.AskJSONWithLimit(ctx, llm.TierCritical, systemPrompt, thesisPrompt, councilPerspectiveMaxTokens, 0.2)
}

func (c *Council) compilePerspectiveJSON(ctx context.Context, archetype, systemPrompt, thesisPrompt, rawResponse string) (string, error) {
	compileCtx, cancel := context.WithTimeout(ctx, councilCompilerTimeout)
	defer cancel()

	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: councilCompilerPrompt},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Council voice: %s\n\nOriginal council system prompt:\n%s\n\nThesis under review:\n%s\n\nCouncil reasoning transcript:\n%s", archetype, systemPrompt, thesisPrompt, truncateForCompiler(rawResponse, 1800))},
		},
		Model:       c.compilerModel,
		Tier:        llm.TierSpeed,
		MaxTokens:   councilCompilerMaxTokens,
		Temperature: 0.0,
		JSONMode:    true,
	}

	resp, err := c.llm.Complete(compileCtx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (c *Council) synthesize(thesis *model.Thesis, results []perspectiveResult) *model.CouncilVerdict {
	if len(results) == 0 {
		return &model.CouncilVerdict{
			Approved: false,
			Perspectives: map[string]string{
				"error": "no archetypes responded",
			},
		}
	}

	perspectives := make(map[string]string, len(results))
	voices := make([]model.CouncilVoiceContribution, 0, len(results))
	totalConvAdj := 0.0
	totalSizeAdj := 0.0
	totalWeight := 0.0
	voteScore := 0.0

	for _, r := range results {
		perspectives[r.name] = r.view
		totalConvAdj += r.conviction * r.weight
		totalSizeAdj += r.size * r.weight
		totalWeight += r.weight
		voteScore += councilVoteScore(r)
		voices = append(voices, model.CouncilVoiceContribution{
			Name:                 r.name,
			Perspective:          r.view,
			Reasoning:            r.reasoning,
			Recommendation:       r.recommendation,
			ConvictionAdjustment: r.conviction,
			SizeAdjustment:       r.size,
			Weight:               r.weight,
			HistoricalAccuracy:   r.accuracy,
			Observations:         r.observations,
		})
	}

	if totalWeight <= 0 {
		totalWeight = float64(len(results))
	}

	avgConvAdj := totalConvAdj / totalWeight
	avgSizeAdj := totalSizeAdj / totalWeight

	if avgSizeAdj <= 0 {
		avgSizeAdj = 1.0
	}

	adjustedConviction := thesis.Conviction + avgConvAdj
	if adjustedConviction > 1.0 {
		adjustedConviction = 1.0
	}
	if adjustedConviction < 0 {
		adjustedConviction = 0
	}

	adjustedSize := thesis.PositionSize * avgSizeAdj

	approved := voteScore > 0 && avgConvAdj > -0.12

	c.log.Info("council verdict",
		"thesis_id", thesis.ID,
		"approved", approved,
		"avg_conviction_adj", avgConvAdj,
		"avg_size_adj", avgSizeAdj,
		"vote_score", voteScore,
		"total_weight", totalWeight,
		"perspectives", len(results),
	)

	return &model.CouncilVerdict{
		Approved:           approved,
		Perspectives:       perspectives,
		Voices:             voices,
		AdjustedSize:       adjustedSize,
		AdjustedConviction: adjustedConviction,
		WeightedVoteScore:  voteScore,
		TotalWeight:        totalWeight,
	}
}

func prosecutionVerdict(p *model.Prosecution) string {
	if p == nil {
		return "not prosecuted"
	}
	return p.Verdict
}

func (c *Council) voiceTelemetry(ctx context.Context, domain string) map[string]model.CouncilVoiceStats {
	if c.telemetry == nil {
		return map[string]model.CouncilVoiceStats{}
	}
	stats, err := c.telemetry.CouncilVoiceTelemetry(ctx, domain)
	if err != nil {
		c.log.Warn("council telemetry lookup failed", "domain", domain, "error", err)
		return map[string]model.CouncilVoiceStats{}
	}
	return stats
}

func normalizeRecommendation(raw string, convictionAdj, sizeAdj float64) model.CouncilRecommendation {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "approve", "support", "buy", "go":
		return model.CouncilApprove
	case "reject", "oppose", "deny", "block":
		return model.CouncilReject
	case "abstain", "wait", "hold", "pass", "flat":
		return model.CouncilAbstain
	}
	if convictionAdj <= -0.05 || sizeAdj < 0.85 {
		return model.CouncilReject
	}
	if convictionAdj >= 0.05 || sizeAdj > 1.05 {
		return model.CouncilApprove
	}
	return model.CouncilAbstain
}

func normalizeSizeAdjustment(size float64) float64 {
	if size <= 0 {
		return 1
	}
	if size < 0.5 {
		return 0.5
	}
	if size > 1.5 {
		return 1.5
	}
	return size
}

func clampCouncilAdjustment(value float64) float64 {
	if value > 0.2 {
		return 0.2
	}
	if value < -0.2 {
		return -0.2
	}
	return value
}

func normalizeVoiceWeight(weight float64) float64 {
	if weight <= 0 {
		return 1
	}
	return math.Max(0.75, math.Min(1.35, weight))
}

func councilVoteScore(result perspectiveResult) float64 {
	weight := normalizeVoiceWeight(result.weight)
	strength := math.Max(math.Abs(result.conviction), math.Abs(result.size-1))
	if strength < 0.05 {
		strength = 0.05
	}
	score := weight * (1 + strength)
	switch result.recommendation {
	case model.CouncilApprove:
		return score
	case model.CouncilReject, model.CouncilAbstain:
		return -score
	default:
		return 0
	}
}
