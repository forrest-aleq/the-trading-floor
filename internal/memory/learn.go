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
	attribution := ensureOutcomeAttribution(thesis, outcome)
	capability := thesis.ExecutionCapability()
	key := belief.CompetenceKey(thesis.DeskID, thesis.Strategy, capability, regime.Key())
	scanKey := ""
	if thesis.Domain != "" {
		scanKey = belief.CompetenceKey(thesis.DeskID, "scan", thesis.Domain, regime.Key())
	}
	thesisKey := belief.CompetenceKey(thesis.DeskID, competenceThesisAssessment, thesis.Strategy, regime.Key())
	timingKey := belief.CompetenceKey(thesis.DeskID, competenceTimingAssessment, thesis.Strategy, regime.Key())
	structureKey := belief.CompetenceKey(thesis.DeskID, competenceStructureSelect, capability, regime.Key())
	executionKey := belief.CompetenceKey(thesis.DeskID, competenceExecutionQuality, capability, regime.Key())
	surpriseKey := belief.CompetenceKey(thesis.DeskID, competenceSurpriseAssess, thesis.Strategy, regime.Key())
	sourceKey := sourceBeliefKeyForThesis(thesis)

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

	updates := make([]model.AttributionUpdate, 0, 6)
	recordUpdate := func(competenceKey, dimension string, score float64, boundary bool) {
		if competenceKey == "" {
			return
		}
		weightedMagnitude := magnitude * math.Abs(clampSigned(score)) * learningWeight(attribution)
		if weightedMagnitude < 0.05 {
			return
		}
		if score >= 0 {
			l.graph.ApplySuccess(competenceKey, weightedMagnitude)
		} else {
			l.graph.ApplyFailure(competenceKey, weightedMagnitude, boundary)
		}
		updates = append(updates, model.AttributionUpdate{
			Key:       competenceKey,
			Dimension: dimension,
			Score:     clampSigned(score),
		})
	}
	recordUpdate(key, "overall", overallAttributionScore(attribution), boundaryViolation)
	recordUpdate(scanKey, "truth_edge", attribution.TruthEdge, boundaryViolation)
	recordUpdate(thesisKey, "truth_edge", attribution.TruthEdge, boundaryViolation)
	recordUpdate(timingKey, "timing_edge", attribution.TimingEdge, boundaryViolation)
	recordUpdate(structureKey, "expression_edge", attribution.ExpressionEdge, boundaryViolation)
	recordUpdate(executionKey, "execution_edge", attribution.ExecutionEdge, boundaryViolation)
	if surpriseScore, ok := surpriseValidationScore(thesis, outcome); ok {
		recordUpdate(surpriseKey, "surprise_validation", surpriseScore, false)
	}
	if sourceKey != "" {
		sourceMagnitude := magnitude * math.Abs(clampSigned(overallAttributionScore(attribution))) * learningWeight(attribution)
		if sourceMagnitude >= 0.05 {
			if overallAttributionScore(attribution) >= 0 {
				l.graph.ApplySourceSuccess(sourceKey, sourceMagnitude)
			} else {
				l.graph.ApplySourceFailure(sourceKey, sourceMagnitude)
			}
			updates = append(updates, model.AttributionUpdate{
				Key:       sourceKey,
				Dimension: "source_edge",
				Score:     clampSigned(overallAttributionScore(attribution)),
			})
		}
	}
	if input := thesis.CollaborationInput; input != nil && input.OriginDesk != "" && input.OriginDesk != thesis.DeskID {
		peerKey := belief.PeerBeliefKey(input.OriginDesk, thesis.DeskID, firstNonEmptyLearn(thesis.Domain, input.OriginDomain), regime.Key())
		peerMagnitude := magnitude * math.Abs(clampSigned(overallAttributionScore(attribution))) * learningWeight(attribution)
		if peerMagnitude >= 0.05 {
			if overallAttributionScore(attribution) >= 0 {
				recovery := input.SocialCost >= 0.20 || input.FaceThreatScore >= 0.20 || input.AppraisalClass == "negative_surprise"
				l.graph.ApplyPeerSuccessWithContext(peerKey, peerMagnitude, recovery, input.SocialCost)
			} else {
				l.graph.ApplyPeerFailureWithContext(peerKey, peerMagnitude, input.FaceThreatScore)
			}
			updates = append(updates, model.AttributionUpdate{
				Key:       peerKey,
				Dimension: "peer_edge",
				Score:     clampSigned(overallAttributionScore(attribution)),
			})
		}
	}
	attribution.CompetenceUpdates = updates

	// Record engram for pattern caching
	if l.engrams != nil && learningWeight(attribution) >= 0.25 {
		intentKey := thesis.Strategy + "_" + capability
		globalContextPattern := thesis.Strategy + "_" + regime.Key()
		deskContextPattern := thesis.DisplaySymbol() + "_" + regime.Key()
		returnPct := 0.0
		if thesis.EntryPrice > 0 {
			returnPct = outcome.RealizedPnL / (thesis.EntryPrice * thesis.PositionSize) * 100
		}
		engramsProfitable := overallAttributionScore(attribution) > 0

		// Layer 1: cross-desk playbook memory.
		l.engrams.Record(
			intentKey,
			globalContextPattern,
			capability,
			"",
			[]string{regime.Volatility, regime.Trend, regime.Risk},
			engramsProfitable,
			returnPct,
		)

		// Layer 2: desk-specific experience.
		l.engrams.Record(
			intentKey,
			deskContextPattern,
			capability,
			thesis.DeskID,
			[]string{regime.Volatility, regime.Trend, regime.Risk},
			engramsProfitable,
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
		"attribution", attributionSummary(attribution),
		"learning_weight", learningWeight(attribution),
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

func firstNonEmptyLearn(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func sourceBeliefKeyForThesis(thesis *model.Thesis) string {
	if thesis == nil || thesis.EvidenceMeta == nil {
		return ""
	}
	meta := thesis.EvidenceMeta
	if meta.SourceOwnerGroup == "" && meta.SourceDomain == "" {
		return ""
	}
	return belief.SourceBeliefKey(
		meta.SourceOwnerGroup,
		meta.SourceDomain,
		firstNonEmptyLearn(thesis.Domain, thesis.Strategy),
		meta.OriginalLanguage,
		meta.OriginRegion,
	)
}
