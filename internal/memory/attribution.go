package memory

import (
	"fmt"
	"math"
	"strings"

	"github.com/hnic/trading-floor/pkg/model"
)

const (
	competenceThesisAssessment = "thesis_assessment"
	competenceTimingAssessment = "timing_assessment"
	competenceStructureSelect  = "structure_selection"
	competenceExecutionQuality = "execution_quality"
)

func ensureOutcomeAttribution(thesis *model.Thesis, outcome *model.ThesisOutcome) *model.OutcomeAttribution {
	if outcome == nil {
		return nil
	}
	if outcome.Attribution == nil {
		outcome.Attribution = inferOutcomeAttribution(thesis, outcome)
	}
	normalizeAttribution(outcome.Attribution)
	if outcome.Attribution.Method == "" {
		outcome.Attribution.Method = "provided"
	}
	return outcome.Attribution
}

func inferOutcomeAttribution(thesis *model.Thesis, outcome *model.ThesisOutcome) *model.OutcomeAttribution {
	attr := &model.OutcomeAttribution{
		Method:  "heuristic",
		Summary: "deterministic post-trade attribution",
	}

	if outcome == nil {
		return attr
	}

	if outcome.Profitable {
		attr.TruthEdge = 0.55
		attr.TimingEdge = 0.45
		attr.ExpressionEdge = 0.40
		attr.ExecutionEdge = 0.35
		attr.LuckEstimate = 0.20
	} else {
		attr.TruthEdge = -0.55
		attr.TimingEdge = -0.45
		attr.ExpressionEdge = -0.40
		attr.ExecutionEdge = -0.35
		attr.LuckEstimate = 0.25
	}

	switch normalizeReason(outcome.ErrorClass) {
	case "thesis_failure":
		attr.TruthEdge = -0.95
		attr.TimingEdge = math.Min(attr.TimingEdge, -0.60)
		attr.ExpressionEdge = math.Min(attr.ExpressionEdge, -0.35)
		attr.ExecutionEdge = math.Max(attr.ExecutionEdge, -0.10)
		attr.LuckEstimate = math.Min(attr.LuckEstimate, 0.10)
		attr.Summary = "thesis failed after live validation"
	case "execution_friction":
		attr.ExecutionEdge = -0.90
		attr.LuckEstimate = math.Max(attr.LuckEstimate, 0.45)
		attr.Summary = "execution friction degraded a live trade"
	case "infrastructure_error":
		attr.TruthEdge = 0
		attr.TimingEdge = 0
		attr.ExpressionEdge = 0
		attr.ExecutionEdge = -1
		attr.LuckEstimate = 0.95
		attr.Summary = "infrastructure failure overwhelmed the outcome"
	case "policy_block":
		attr.TruthEdge = 0
		attr.TimingEdge = 0
		attr.ExpressionEdge = 0
		attr.ExecutionEdge = 0
		attr.LuckEstimate = 1
		attr.Summary = "policy block prevented live expression"
	case "market_halt":
		attr.TruthEdge = 0
		attr.TimingEdge = -0.15
		attr.ExpressionEdge = 0
		attr.ExecutionEdge = -0.30
		attr.LuckEstimate = 0.90
		attr.Summary = "market halt dominated the outcome"
	}

	switch normalizeReason(outcome.ExitReason) {
	case "target_hit", "target", "profit_target", "take_profit":
		attr.TruthEdge = math.Max(attr.TruthEdge, 0.85)
		attr.TimingEdge = math.Max(attr.TimingEdge, 0.75)
		attr.ExpressionEdge = math.Max(attr.ExpressionEdge, 0.65)
		attr.ExecutionEdge = math.Max(attr.ExecutionEdge, 0.55)
		attr.LuckEstimate = math.Min(attr.LuckEstimate, 0.15)
		attr.Summary = "thesis reached its profit objective"
	case "stop_loss", "stop", "kill_rule", "thesis_invalidated":
		attr.TruthEdge = math.Min(attr.TruthEdge, -0.85)
		attr.TimingEdge = math.Min(attr.TimingEdge, -0.65)
		attr.ExpressionEdge = math.Min(attr.ExpressionEdge, -0.50)
		attr.ExecutionEdge = math.Min(attr.ExecutionEdge, -0.35)
		attr.LuckEstimate = math.Min(attr.LuckEstimate, 0.10)
		attr.Summary = "trade failed against its risk boundary"
	case "timeout", "time_exit", "expiry":
		if outcome.Profitable {
			attr.TimingEdge = math.Min(attr.TimingEdge, 0.20)
			attr.ExpressionEdge = math.Min(attr.ExpressionEdge, 0.30)
			attr.Summary = "trade worked, but timing or carry limited the payoff"
		} else {
			attr.TimingEdge = math.Min(attr.TimingEdge, -0.45)
			attr.ExpressionEdge = math.Min(attr.ExpressionEdge, -0.25)
			attr.Summary = "time decay or delayed confirmation hurt the trade"
		}
	}

	if thesis != nil && thesis.TimeHorizon > 0 && outcome.HoldingHours > 0 {
		horizonRatio := outcome.HoldingHours / thesis.TimeHorizon.Hours()
		if outcome.Profitable && horizonRatio <= 0.35 {
			attr.TimingEdge += 0.10
		}
		if !outcome.Profitable && horizonRatio >= 1.50 {
			attr.TimingEdge -= 0.15
		}
		if !outcome.Profitable && horizonRatio <= 0.10 {
			attr.ExecutionEdge -= 0.05
		}
	}

	if thesis != nil && thesis.IsMultiLeg() {
		if outcome.Profitable {
			attr.ExpressionEdge += 0.10
		} else {
			attr.ExpressionEdge -= 0.10
		}
	}

	if outcome.Profitable && outcome.RiskReward >= 1.5 {
		attr.ExpressionEdge += 0.10
	}
	if !outcome.Profitable && outcome.RiskReward > 0 && outcome.RiskReward < 0.5 {
		attr.ExpressionEdge -= 0.10
	}

	normalizeAttribution(attr)
	return attr
}

func normalizeAttribution(attr *model.OutcomeAttribution) {
	if attr == nil {
		return
	}
	attr.TruthEdge = clampSigned(attr.TruthEdge)
	attr.TimingEdge = clampSigned(attr.TimingEdge)
	attr.ExpressionEdge = clampSigned(attr.ExpressionEdge)
	attr.ExecutionEdge = clampSigned(attr.ExecutionEdge)
	attr.LuckEstimate = clamp01(attr.LuckEstimate)
}

func learningWeight(attr *model.OutcomeAttribution) float64 {
	if attr == nil {
		return 1
	}
	weight := 1 - clamp01(attr.LuckEstimate)
	if weight < 0.05 {
		return 0
	}
	return weight
}

func overallAttributionScore(attr *model.OutcomeAttribution) float64 {
	if attr == nil {
		return 0
	}
	score := (attr.TruthEdge * 0.40) +
		(attr.TimingEdge * 0.20) +
		(attr.ExpressionEdge * 0.25) +
		(attr.ExecutionEdge * 0.15)
	return clampSigned(score)
}

func normalizeReason(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func attributionSummary(attr *model.OutcomeAttribution) string {
	if attr == nil {
		return ""
	}
	return fmt.Sprintf(
		"truth=%.2f timing=%.2f expression=%.2f execution=%.2f luck=%.2f",
		attr.TruthEdge,
		attr.TimingEdge,
		attr.ExpressionEdge,
		attr.ExecutionEdge,
		attr.LuckEstimate,
	)
}

func clampSigned(value float64) float64 {
	if value > 1 {
		return 1
	}
	if value < -1 {
		return -1
	}
	return value
}

func clamp01(value float64) float64 {
	if value > 1 {
		return 1
	}
	if value < 0 {
		return 0
	}
	return value
}
