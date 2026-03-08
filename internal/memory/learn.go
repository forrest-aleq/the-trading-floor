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
	log    *slog.Logger
	graph  *belief.Graph
}

func NewLearnWorker(graph *belief.Graph) *LearnWorker {
	return &LearnWorker{
		log:   slog.Default().With("component", "learn-worker"),
		graph: graph,
	}
}

// ProcessOutcome handles a completed trade
func (l *LearnWorker) ProcessOutcome(thesis *model.Thesis, outcome *model.ThesisOutcome, regime model.Regime) {
	key := belief.CompetenceKey(thesis.DeskID, thesis.Strategy, thesis.Instrument.SecType, regime.Key())

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
	} else {
		l.graph.ApplyFailure(key, magnitude, boundaryViolation)
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
