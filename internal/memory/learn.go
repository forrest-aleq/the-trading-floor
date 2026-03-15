package memory

import (
	"log/slog"
	"math"

	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/pkg/model"
)

// LearnWorker processes trade outcomes and updates beliefs.
// Ported from MARS learn-worker.js
type LearnWorker struct {
	log     *slog.Logger
	graph   *belief.Graph
	engrams *EngramStore
}

func NewLearnWorker(graph *belief.Graph, engrams *EngramStore) *LearnWorker {
	return &LearnWorker{
		log:     slog.Default().With("component", "learn-worker"),
		graph:   graph,
		engrams: engrams,
	}
}

// ProcessOutcome handles a completed trade
func (l *LearnWorker) ProcessOutcome(thesis *model.Thesis, outcome *model.ThesisOutcome, regime model.Regime) {
	capability := thesis.ExecutionCapability()
	key := belief.CompetenceKey(thesis.DeskID, thesis.Strategy, capability, regime.Key())
	scanKey := ""
	if thesis.Domain != "" {
		scanKey = belief.CompetenceKey(thesis.DeskID, "scan", thesis.Domain, regime.Key())
	}

	// Calculate magnitude: realized_return / expected_risk, clamped to [-2, 2]
	expectedRisk := math.Abs(thesis.EntryPrice - thesis.StopLoss)
	if expectedRisk == 0 {
		expectedRisk = thesis.EntryPrice * 0.02 // Default 2% risk if no stop
	}
	magnitude := math.Abs(outcome.RealizedPnL) / (expectedRisk * thesis.PositionSize)
	magnitude = clip(magnitude, 0, 2.0)

	// Classify the outcome
	switch outcome.ErrorClass {
	case "infrastructure_error", "market_halt":
		// Non-penalizing — don't update beliefs
		l.log.Info("outcome skipped (non-penalizing)",
			"thesis_id", thesis.ID,
			"error_class", outcome.ErrorClass,
		)
		return

	case "policy_block":
		// Record but don't update beliefs
		l.log.Info("outcome recorded (policy block)",
			"thesis_id", thesis.ID,
		)
		return
	}

	// Check for boundary violation (blew through stop loss significantly)
	boundaryViolation := false
	if outcome.RealizedPnL < 0 && expectedRisk > 0 {
		lossRatio := math.Abs(outcome.RealizedPnL) / (expectedRisk * thesis.PositionSize)
		if lossRatio > 2.0 {
			boundaryViolation = true
		}
	}

	if outcome.Profitable {
		l.graph.ApplySuccess(key, magnitude)
		if scanKey != "" {
			l.graph.ApplySuccess(scanKey, magnitude)
		}
	} else {
		l.graph.ApplyFailure(key, magnitude, boundaryViolation)
		if scanKey != "" {
			l.graph.ApplyFailure(scanKey, magnitude, boundaryViolation)
		}
	}

	// Record engram for pattern caching
	if l.engrams != nil {
		intentKey := thesis.Strategy + "_" + capability
		globalContextPattern := thesis.Strategy + "_" + regime.Key()
		deskContextPattern := thesis.DisplaySymbol() + "_" + regime.Key()
		returnPct := 0.0
		if thesis.EntryPrice > 0 {
			returnPct = outcome.RealizedPnL / (thesis.EntryPrice * thesis.PositionSize) * 100
		}

		// Layer 1: cross-desk playbook memory.
		l.engrams.Record(
			intentKey,
			globalContextPattern,
			thesis.Strategy,
			"",
			[]string{regime.Volatility, regime.Trend, regime.Risk},
			outcome.Profitable,
			returnPct,
		)

		// Layer 2: desk-specific experience.
		l.engrams.Record(
			intentKey,
			deskContextPattern,
			thesis.Strategy,
			thesis.DeskID,
			[]string{regime.Volatility, regime.Trend, regime.Risk},
			outcome.Profitable,
			returnPct,
		)
	}

	l.log.Info("outcome processed",
		"thesis_id", thesis.ID,
		"desk", thesis.DeskID,
		"strategy", thesis.Strategy,
		"profitable", outcome.Profitable,
		"pnl", outcome.RealizedPnL,
		"magnitude", magnitude,
		"boundary_violation", boundaryViolation,
		"regime", regime.Key(),
	)
}

func clip(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
