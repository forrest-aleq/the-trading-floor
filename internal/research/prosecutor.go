package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/institutional"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

// Prosecutor tries to kill every thesis before it can trade.
// Uses the critical-tier LLM (Claude Sonnet) for maximum reasoning depth.
type Prosecutor struct {
	log            *slog.Logger
	llm            *llm.Router
	selectedModel  string
	responseMode   structuredResponseMode
	compilerModel  string
	systemPrompt   string
	thoughtPrefix  string
	compilerPrompt string
}

func NewProsecutor(llmRouter *llm.Router) *Prosecutor {
	selectedModel := criticalSelectedModel()
	policy := activePromptPolicy()
	responseMode := strings.TrimSpace(os.Getenv("PROSECUTION_RESPONSE_MODE"))
	if responseMode == "" {
		responseMode = "json"
	}
	return &Prosecutor{
		log:            slog.Default().With("component", "prosecutor"),
		llm:            llmRouter,
		selectedModel:  selectedModel,
		responseMode:   detectStructuredResponseMode(responseMode, selectedModel),
		compilerModel:  structuredCompilerModel("PROSECUTION_COMPILER_MODEL"),
		systemPrompt:   policy.prosecutionPrompt,
		thoughtPrefix:  policy.prosecutionThoughtPrefix,
		compilerPrompt: policy.prosecutionCompilerPrompt,
	}
}

var (
	prosecutionMaxTokens         = readStructuredIntEnv("PROSECUTION_MAX_TOKENS", 768)
	prosecutionCompilerTimeout   = readStructuredDurationEnv("PROSECUTION_COMPILER_TIMEOUT", 25*time.Second)
	prosecutionCompilerMaxTokens = readStructuredIntEnv("PROSECUTION_COMPILER_MAX_TOKENS", 900)
)

// Challenge attempts to kill a thesis
func (p *Prosecutor) Challenge(ctx context.Context, thesis *model.Thesis) *model.Prosecution {
	if fastPathMode := kalshiDeterministicFastPathMode(); fastPathMode != "" && thesis != nil && thesis.PrimaryInstrument().IsKalshi() {
		p.log.Info("deterministic Kalshi prosecution passed",
			"thesis_id", thesis.ID,
			"symbol", thesis.DisplaySymbol(),
			"fast_path_mode", fastPathMode,
		)
		return &model.Prosecution{
			Verdict:    "survived",
			BearArgs:   deterministicKalshiProsecutionBearArgs(fastPathMode),
			Confidence: 0,
		}
	}

	prompt := p.buildProsecutionPrompt(thesis)

	resp, err := p.askProsecutionWithFallbackMode(ctx, prompt)
	if err != nil {
		p.log.Error("prosecution LLM error", "error", err, "thesis_id", thesis.ID)
		// On LLM error, default to weakened (conservative)
		return &model.Prosecution{
			Verdict:    "weakened",
			BearArgs:   []string{"prosecution LLM unavailable — defaulting to conservative"},
			Confidence: -0.1,
		}
	}

	cleaned, cleanErr := extractStructuredJSON(resp)
	if cleanErr != nil {
		if p.compilerModel != "" {
			if compiled, compileErr := p.compileProsecutionJSON(ctx, prompt, resp); compileErr == nil {
				if compiledJSON, extractErr := extractStructuredJSON(compiled); extractErr == nil {
					cleaned = compiledJSON
					cleanErr = nil
					p.log.Info("prosecution compiler recovered structured verdict",
						"thesis_id", thesis.ID,
						"compiler_model", p.compilerModel,
					)
				}
			} else {
				p.log.Warn("prosecution compiler fallback failed",
					"thesis_id", thesis.ID,
					"compiler_model", p.compilerModel,
					"error", compileErr,
				)
			}
		}
	}

	if cleanErr != nil {
		p.log.Error("prosecution JSON extraction failed",
			"error", cleanErr,
			"response_len", len(resp),
			"response_excerpt", truncateForLog(resp, 320),
		)
		return &model.Prosecution{
			Verdict:    "weakened",
			BearArgs:   []string{"prosecution JSON extraction failed — defaulting to conservative"},
			Confidence: -0.1,
		}
	}

	cleaned = normalizeProsecutionJSON(cleaned)

	var result prosecutionResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		p.log.Error("prosecution parse error",
			"error", err,
			"response_excerpt", truncateForLog(cleaned, 320),
		)
		return &model.Prosecution{
			Verdict:    "weakened",
			BearArgs:   []string{"prosecution parse error — defaulting to conservative"},
			Confidence: -0.1,
		}
	}

	prosecution := &model.Prosecution{
		Verdict:    result.Verdict,
		BearArgs:   result.BearArgs,
		Analogues:  result.HistoricalAnalogues,
		Confidence: result.ConfidenceAdjustment,
	}

	p.log.Info("prosecution complete",
		"thesis_id", thesis.ID,
		"verdict", prosecution.Verdict,
		"bear_args", len(prosecution.BearArgs),
		"confidence_adj", prosecution.Confidence,
	)

	return prosecution
}

func deterministicKalshiProsecutionBearArgs(mode string) []string {
	if mode == "live" {
		return []string{
			"Live deterministic Kalshi prosecution path: neutral pass while OpenRouter capacity is constrained.",
			"Execution remains limited by Kalshi live safety caps, duplicate cooldown, and exchange validation.",
		}
	}
	return []string{
		"Paper-discovery deterministic prosecution; live deployment still requires model-backed prosecution.",
	}
}

func (p *Prosecutor) buildProsecutionPrompt(thesis *model.Thesis) string {
	return "Thesis to prosecute:\n\n" + institutional.BuildThesisContext(thesis, institutional.ThesisContextOptions{
		IncludeInstitutional: true,
		IncludeEvidence:      true,
		IncludeCounterArgs:   true,
		IncludeQuant:         true,
	})
}

func (p *Prosecutor) askProsecutionWithFallbackMode(ctx context.Context, prompt string) (string, error) {
	resp, retried, err := askStructuredWithRetry(ctx, p.llm, llm.TierCritical, p.responseMode, p.systemPrompt, p.thoughtPrefix, prompt, prosecutionMaxTokens, 0.2)
	if retried {
		p.log.Info("prosecution structured retry recovered terminal JSON miss",
			"model", p.selectedModel,
		)
	}
	return resp, err
}

func (p *Prosecutor) compileProsecutionJSON(ctx context.Context, originalPrompt, rawResponse string) (string, error) {
	compileCtx, cancel := withStructuredBudgetFraction(ctx, prosecutionCompilerTimeout, 1.0)
	defer cancel()

	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: p.compilerPrompt},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Original prosecution task:\n%s\n\nProsecution reasoning transcript:\n%s", originalPrompt, truncateForCompiler(rawResponse, 1800))},
		},
		Model:       p.compilerModel,
		Tier:        llm.TierSpeed,
		MaxTokens:   prosecutionCompilerMaxTokens,
		Temperature: 0.0,
		JSONMode:    true,
	}

	resp, err := p.llm.Complete(compileCtx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

type prosecutionResult struct {
	Verdict              string   `json:"verdict"`
	BearArgs             []string `json:"bear_args"`
	MissingData          []string `json:"missing_data"`
	HistoricalAnalogues  []string `json:"historical_analogues"`
	CrowdedScore         float64  `json:"crowded_score"`
	ConfidenceAdjustment float64  `json:"confidence_adjustment"`
	Reasoning            string   `json:"reasoning"`
}

func normalizeProsecutionJSON(cleaned string) string {
	trimmed := strings.TrimSpace(cleaned)
	if !strings.HasPrefix(trimmed, "[") {
		return cleaned
	}

	var bearArgs []string
	if err := json.Unmarshal([]byte(trimmed), &bearArgs); err != nil || len(bearArgs) == 0 {
		return cleaned
	}

	normalized, err := json.Marshal(prosecutionResult{
		Verdict:              "weakened",
		BearArgs:             bearArgs,
		ConfidenceAdjustment: -0.1,
		Reasoning:            "normalized from array-only prosecution response",
	})
	if err != nil {
		return cleaned
	}
	return string(normalized)
}
