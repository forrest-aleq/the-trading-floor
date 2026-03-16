package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

// Prosecutor tries to kill every thesis before it can trade.
// Uses the critical-tier LLM (Claude Sonnet) for maximum reasoning depth.
type Prosecutor struct {
	log *slog.Logger
	llm *llm.Router
}

func NewProsecutor(llmRouter *llm.Router) *Prosecutor {
	return &Prosecutor{
		log: slog.Default().With("component", "prosecutor"),
		llm: llmRouter,
	}
}

const prosecutionPrompt = `You are an adversarial prosecutor reviewing a trading thesis. Your job is to DESTROY this thesis. Find every reason it should not be traded.

You must:
1. Generate 5-7 bear arguments against this thesis
2. Identify historical analogues where similar trades FAILED
3. Check if this is a crowded trade (if everyone sees it, it's priced in)
4. Stress test each assumption - what if they're wrong?
5. Identify what data is MISSING that would be needed to have real conviction
6. Consider second-order effects the thesis might have missed

Your verdict options:
- KILLED: Fatal flaws found. Do not trade.
- WEAKENED: Significant concerns. Reduce conviction and position size.
- SURVIVED: Arguments found but thesis core holds. Proceed with caution.
- STRENGTHENED: Prosecution revealed additional supporting evidence.

Be ruthlessly honest. A trade that survives your prosecution has earned its conviction.

Respond in JSON:
{
  "verdict": "killed|weakened|survived|strengthened",
  "bear_args": ["...", "..."],
  "missing_data": ["...", "..."],
  "historical_analogues": ["...", "..."],
  "crowded_score": 0.0-1.0,
  "confidence_adjustment": -0.3 to +0.1,
  "reasoning": "..."
}`

const prosecutionMaxTokens = 768

// Challenge attempts to kill a thesis
func (p *Prosecutor) Challenge(ctx context.Context, thesis *model.Thesis) *model.Prosecution {
	prompt := fmt.Sprintf(`Thesis to prosecute:

Symbol: %s (%s)
Direction: %s
Strategy: %s
Conviction: %.2f
Entry: %.2f / Target: %.2f / Stop: %.2f
Time Horizon: %s

Evidence:
%s

Counter Arguments Already Considered:
%s

Quant Metrics:
%s`,
		thesis.Instrument.Symbol, thesis.Instrument.SecType,
		thesis.Direction, thesis.Strategy,
		thesis.Conviction,
		thesis.EntryPrice, thesis.TargetPrice, thesis.StopLoss,
		thesis.TimeHorizon,
		formatEvidence(thesis.Evidence),
		formatCounterArgs(thesis.CounterArgs),
		formatQuantMetrics(thesis.QuantMetrics),
	)

	resp, err := p.llm.AskJSONWithLimit(ctx, llm.TierCritical, prosecutionPrompt, prompt, prosecutionMaxTokens, 0.2)
	if err != nil {
		p.log.Error("prosecution LLM error", "error", err, "thesis_id", thesis.ID)
		// On LLM error, default to weakened (conservative)
		return &model.Prosecution{
			Verdict:    "weakened",
			BearArgs:   []string{"prosecution LLM unavailable — defaulting to conservative"},
			Confidence: -0.1,
		}
	}

	cleaned, cleanErr := llm.ExtractJSON(resp)
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

type prosecutionResult struct {
	Verdict              string   `json:"verdict"`
	BearArgs             []string `json:"bear_args"`
	MissingData          []string `json:"missing_data"`
	HistoricalAnalogues  []string `json:"historical_analogues"`
	CrowdedScore         float64  `json:"crowded_score"`
	ConfidenceAdjustment float64  `json:"confidence_adjustment"`
	Reasoning            string   `json:"reasoning"`
}

func formatEvidence(evidence []model.Evidence) string {
	var s string
	for i, e := range evidence {
		s += fmt.Sprintf("  %d. %s (weight: %.1f)\n", i+1, e.Content, e.Weight)
	}
	return s
}

func formatCounterArgs(args []string) string {
	var s string
	for i, a := range args {
		s += fmt.Sprintf("  %d. %s\n", i+1, a)
	}
	return s
}

func formatQuantMetrics(metrics *model.QuantMetrics) string {
	if metrics == nil {
		return "  unavailable\n"
	}

	s := fmt.Sprintf("  Method: %s\n  Defined risk: %t\n", metrics.Method, metrics.DefinedRisk)
	if metrics.MaxLoss > 0 {
		s += fmt.Sprintf("  Max loss: %.2f\n", metrics.MaxLoss)
	}
	if metrics.MaxGain > 0 {
		s += fmt.Sprintf("  Max gain: %.2f\n", metrics.MaxGain)
	}
	if metrics.Breakeven != 0 {
		s += fmt.Sprintf("  Breakeven: %.2f\n", metrics.Breakeven)
	}
	if metrics.MarginEstimate > 0 {
		s += fmt.Sprintf("  Margin estimate: %.2f\n", metrics.MarginEstimate)
	}
	if metrics.RewardToRisk > 0 {
		s += fmt.Sprintf("  Reward/risk: %.2f\n", metrics.RewardToRisk)
	}
	if len(metrics.Warnings) > 0 {
		s += fmt.Sprintf("  Warnings: %v\n", metrics.Warnings)
	}
	return s
}
