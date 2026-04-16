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

	"github.com/hnic/trading-floor/internal/institutional"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

// Council convenes multiple strategy archetypes to debate large positions (>2% of portfolio).
// Each archetype evaluates independently, then the council synthesizes.
type Council struct {
	log            *slog.Logger
	llm            *llm.Router
	archetypes     []Archetype
	telemetry      VoiceTelemetryProvider
	selectedModel  string
	responseMode   structuredResponseMode
	compilerModel  string
	thoughtPrefix  string
	compilerPrompt string
}

var (
	councilPerspectiveMaxTokens = readStructuredIntEnv("COUNCIL_MAX_TOKENS", 384)
	councilPerspectiveTimeout   = readStructuredDurationEnv("COUNCIL_TIMEOUT", 25*time.Second)
	councilCompilerTimeout      = readStructuredDurationEnv("COUNCIL_COMPILER_TIMEOUT", 25*time.Second)
	councilCompilerMaxTokens    = readStructuredIntEnv("COUNCIL_COMPILER_MAX_TOKENS", 600)
)

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
	policy := activePromptPolicy()
	return &Council{
		log:            slog.Default().With("component", "council"),
		llm:            llmRouter,
		selectedModel:  selectedModel,
		responseMode:   detectStructuredResponseMode(os.Getenv("COUNCIL_RESPONSE_MODE"), selectedModel),
		compilerModel:  structuredCompilerModel("COUNCIL_COMPILER_MODEL"),
		thoughtPrefix:  policy.councilThoughtPrefix,
		compilerPrompt: policy.councilCompilerPrompt,
		archetypes:     policy.councilArchetypes,
	}
}

func (c *Council) SetVoiceTelemetryProvider(provider VoiceTelemetryProvider) {
	c.telemetry = provider
}

// Debate convenes all archetypes to evaluate a thesis in parallel.
func (c *Council) Debate(ctx context.Context, thesis *model.Thesis) *model.CouncilVerdict {
	telemetry := c.voiceTelemetry(ctx, thesis.Domain)
	thesisPrompt := c.buildCouncilThesisPrompt(thesis)

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

func (c *Council) buildCouncilThesisPrompt(thesis *model.Thesis) string {
	return "Thesis under council review:\n\n" + institutional.BuildThesisContext(thesis, institutional.ThesisContextOptions{
		IncludeInstitutional: true,
		IncludeEvidence:      true,
		IncludeCounterArgs:   true,
		IncludeProsecution:   true,
		IncludeQuant:         true,
	})
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
	resp, retried, err := askStructuredWithRetry(ctx, c.llm, llm.TierCritical, c.responseMode, systemPrompt, c.thoughtPrefix, thesisPrompt, councilPerspectiveMaxTokens, 0.2)
	if retried {
		c.log.Info("council structured retry recovered terminal JSON miss",
			"model", c.selectedModel,
		)
	}
	return resp, err
}

func (c *Council) compilePerspectiveJSON(ctx context.Context, archetype, systemPrompt, thesisPrompt, rawResponse string) (string, error) {
	compileCtx, cancel := context.WithTimeout(ctx, councilCompilerTimeout)
	defer cancel()

	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: c.compilerPrompt},
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
