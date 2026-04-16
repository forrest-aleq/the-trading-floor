package institutional

import (
	"fmt"
	"math"
	"strings"

	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

func EnrichSignalCognition(sig signal.Signal, domain string, input *model.CollaborationInput) signal.Signal {
	expectation := BuildExpectationState(sig, domain, input)
	appraisal := BuildAppraisalState(sig, expectation, input)
	actionSelection := BuildActionSelectionState(expectation, appraisal, input)
	sig.Expectation = expectation
	sig.Appraisal = appraisal
	sig.ActionSelection = actionSelection
	return sig
}

func BuildExpectationState(sig signal.Signal, domain string, input *model.CollaborationInput) *model.ExpectationState {
	meta := sig.EvidenceMeta
	urgency := clamp01(sig.Urgency)
	sourceTrust := evidenceTrust(meta)
	freshness := freshnessScore(meta)
	lead := leadTimeScore(meta)
	fact := confidenceScore(meta, func(v *evidence.ConfidenceVector) float64 { return v.FactConfidence }, sourceTrust)
	marketMap := confidenceScore(meta, func(v *evidence.ConfidenceVector) float64 { return v.MarketMappingConfidence }, evidenceScore(meta))
	novelty := confidenceScore(meta, func(v *evidence.ConfidenceVector) float64 { return v.NoveltyConfidence }, deriveNovelty(sig))
	execution := confidenceScore(meta, func(v *evidence.ConfidenceVector) float64 { return v.ExecutionConfidence }, urgency)
	peerTrust := 0.55
	if input != nil && input.RelationshipTrust > 0 {
		peerTrust = input.RelationshipTrust
	}

	reliability := clamp01(0.45*sourceTrust + 0.25*fact + 0.20*freshness + 0.10*peerTrust)
	tradability := clamp01(0.40*marketMap + 0.25*urgency + 0.20*execution + 0.15*lead)
	importance := clamp01(0.40*urgency + 0.25*reliability + 0.20*novelty + 0.15*lead)
	action := expectationAction(importance, reliability, tradability)

	basis := []string{
		fmt.Sprintf("urgency=%.2f", urgency),
		fmt.Sprintf("source_trust=%.2f", sourceTrust),
		fmt.Sprintf("freshness=%.2f", freshness),
		fmt.Sprintf("market_map=%.2f", marketMap),
	}
	if input != nil && input.OriginDesk != "" {
		basis = append(basis, fmt.Sprintf("peer_trust=%.2f", peerTrust))
	}

	return &model.ExpectationState{
		Domain:               strings.TrimSpace(domain),
		PredictedImportance:  importance,
		PredictedReliability: reliability,
		PredictedTradability: tradability,
		PredictedNovelty:     novelty,
		PredictedDirection:   predictedDirection(sig),
		PredictedAction:      action,
		Basis:                basis,
	}
}

func BuildAppraisalState(sig signal.Signal, expectation *model.ExpectationState, input *model.CollaborationInput) *model.AppraisalState {
	if expectation == nil {
		expectation = BuildExpectationState(sig, "", input)
	}
	meta := sig.EvidenceMeta
	sourceTrust := evidenceTrust(meta)
	freshness := freshnessScore(meta)
	fact := confidenceScore(meta, func(v *evidence.ConfidenceVector) float64 { return v.FactConfidence }, sourceTrust)
	marketMap := confidenceScore(meta, func(v *evidence.ConfidenceVector) float64 { return v.MarketMappingConfidence }, evidenceScore(meta))
	observedReliability := clamp01(0.35*evidenceScore(meta) + 0.25*fact + 0.20*marketMap + 0.20*freshness)
	gap := observedReliability - expectation.PredictedReliability
	violationScore := clamp01(math.Abs(gap))
	violationClass := "aligned"
	if violationScore >= 0.12 {
		if gap > 0 {
			violationClass = "positive_surprise"
		} else {
			violationClass = "negative_surprise"
		}
	}

	power, distance, rank := appraisalModerators(sig, expectation, input)
	relationshipHealth := relationshipHealth(expectation.PredictedNovelty, power, distance)
	if input != nil && input.RelationshipHealth > 0 {
		relationshipHealth = clamp01((relationshipHealth + input.RelationshipHealth) / 2)
	}
	negativeBias := violationScore
	if violationClass == "positive_surprise" {
		negativeBias *= 0.35
	}
	faceThreat := 0.0
	if input != nil {
		faceThreat = clamp01(negativeBias * (0.45*power + 0.30*distance + 0.25*rank))
	}
	recoveryBuffer := 0.0
	if input != nil && input.RecoveryScore > 0 {
		recoveryBuffer = input.RecoveryScore * 0.35
	}
	socialCost := clamp01(faceThreat + (1-relationshipHealth)*0.5 - recoveryBuffer)
	actionPressure := clamp01(max3(expectation.PredictedTradability, clamp01(sig.Urgency), rank*(1-socialCost*0.4)))

	basis := []string{
		fmt.Sprintf("observed_reliability=%.2f", observedReliability),
		fmt.Sprintf("gap=%.2f", gap),
		fmt.Sprintf("power=%.2f", power),
		fmt.Sprintf("distance=%.2f", distance),
		fmt.Sprintf("rank=%.2f", rank),
	}

	return &model.AppraisalState{
		Domain:              expectation.Domain,
		ObservedReliability: observedReliability,
		ExpectationGap:      gap,
		ViolationScore:      violationScore,
		ViolationClass:      violationClass,
		Power:               power,
		Distance:            distance,
		Rank:                rank,
		FaceThreatScore:     faceThreat,
		SocialCost:          socialCost,
		ActionPressure:      actionPressure,
		RelationshipHealth:  relationshipHealth,
		Basis:               basis,
	}
}

func BuildActionSelectionState(expectation *model.ExpectationState, appraisal *model.AppraisalState, input *model.CollaborationInput) *model.ActionSelectionState {
	if expectation == nil || appraisal == nil {
		return nil
	}

	peerTrust := 0.0
	requestedAction := ""
	if input != nil {
		peerTrust = clamp01(input.RelationshipTrust)
		requestedAction = strings.TrimSpace(strings.ToLower(input.RequestedAction))
	}

	baseSuccessProbability := clamp01(
		0.35*expectation.PredictedReliability +
			0.30*expectation.PredictedTradability +
			0.20*appraisal.ActionPressure +
			0.15*peerTrust,
	)

	policy := activeActionPolicy()
	best := model.ActionSelectionState{
		Domain:             expectation.Domain,
		RecommendedAction:  "ignore",
		SuccessProbability: baseSuccessProbability,
		GoalValue:          0,
		SocialCost:         appraisal.SocialCost,
		ExpectedUtility:    -1,
	}
	for _, rule := range policy.Actions {
		matchScore := 0.0
		if requestedAction != "" && requestedAction == rule.Name {
			matchScore = 1.0
		}
		successProbability := clamp01(baseSuccessProbability + matchScore*rule.PeerWeight*0.20)
		goalValue := clamp01(rule.BaseGoalValue + 0.20*expectation.PredictedImportance + 0.15*appraisal.ActionPressure + matchScore*rule.PeerWeight)
		thresholdPenalty := 0.0
		if expectation.PredictedImportance < rule.ImportanceThreshold {
			thresholdPenalty += rule.ImportanceThreshold - expectation.PredictedImportance
		}
		if expectation.PredictedReliability < rule.ReliabilityThreshold {
			thresholdPenalty += rule.ReliabilityThreshold - expectation.PredictedReliability
		}
		if expectation.PredictedTradability < rule.TradabilityThreshold {
			thresholdPenalty += rule.TradabilityThreshold - expectation.PredictedTradability
		}
		thresholdPenalty = clamp01(thresholdPenalty)
		expectedUtility := successProbability*goalValue + appraisePressureBonus(appraisal.ActionPressure, rule.PressureWeight) - appraisal.SocialCost*rule.SocialPenaltyWeight - thresholdPenalty
		if expectedUtility > best.ExpectedUtility {
			best = model.ActionSelectionState{
				Domain:               expectation.Domain,
				RecommendedAction:    rule.Name,
				SuccessProbability:   successProbability,
				GoalValue:            goalValue,
				SocialCost:           appraisal.SocialCost,
				ExpectedUtility:      expectedUtility,
				RequestedActionMatch: matchScore,
				Basis: []string{
					fmt.Sprintf("importance=%.2f", expectation.PredictedImportance),
					fmt.Sprintf("reliability=%.2f", expectation.PredictedReliability),
					fmt.Sprintf("tradability=%.2f", expectation.PredictedTradability),
					fmt.Sprintf("action_pressure=%.2f", appraisal.ActionPressure),
					fmt.Sprintf("social_cost=%.2f", appraisal.SocialCost),
					fmt.Sprintf("peer_trust=%.2f", peerTrust),
					fmt.Sprintf("threshold_penalty=%.2f", thresholdPenalty),
				},
			}
		}
	}
	best.ExpectedUtility = clampSigned(best.ExpectedUtility)
	return &best
}

func BuildExpectationContext(expectation *model.ExpectationState, indent string) string {
	if expectation == nil {
		return ""
	}
	lines := []string{
		"Expectation context:",
		fmt.Sprintf("%sexpectation.domain=%s", indent, expectation.Domain),
		fmt.Sprintf("%sexpectation.importance=%.2f", indent, expectation.PredictedImportance),
		fmt.Sprintf("%sexpectation.reliability=%.2f", indent, expectation.PredictedReliability),
		fmt.Sprintf("%sexpectation.tradability=%.2f", indent, expectation.PredictedTradability),
		fmt.Sprintf("%sexpectation.novelty=%.2f", indent, expectation.PredictedNovelty),
		fmt.Sprintf("%sexpectation.direction=%s", indent, expectation.PredictedDirection),
		fmt.Sprintf("%sexpectation.action=%s", indent, expectation.PredictedAction),
	}
	if len(expectation.Basis) > 0 {
		lines = append(lines, fmt.Sprintf("%sexpectation.basis=%s", indent, strings.Join(SampleStrings(expectation.Basis, 6), "; ")))
	}
	return strings.Join(lines, "\n")
}

func BuildAppraisalContext(appraisal *model.AppraisalState, indent string) string {
	if appraisal == nil {
		return ""
	}
	lines := []string{
		"Appraisal context:",
		fmt.Sprintf("%sappraisal.domain=%s", indent, appraisal.Domain),
		fmt.Sprintf("%sappraisal.class=%s", indent, appraisal.ViolationClass),
		fmt.Sprintf("%sappraisal.violation_score=%.2f", indent, appraisal.ViolationScore),
		fmt.Sprintf("%sappraisal.expectation_gap=%.2f", indent, appraisal.ExpectationGap),
		fmt.Sprintf("%sappraisal.action_pressure=%.2f", indent, appraisal.ActionPressure),
	}
	if appraisal.Power > 0 || appraisal.Distance > 0 || appraisal.Rank > 0 {
		lines = append(lines,
			fmt.Sprintf("%sappraisal.power=%.2f", indent, appraisal.Power),
			fmt.Sprintf("%sappraisal.distance=%.2f", indent, appraisal.Distance),
			fmt.Sprintf("%sappraisal.rank=%.2f", indent, appraisal.Rank),
			fmt.Sprintf("%sappraisal.face_threat=%.2f", indent, appraisal.FaceThreatScore),
			fmt.Sprintf("%sappraisal.social_cost=%.2f", indent, appraisal.SocialCost),
			fmt.Sprintf("%sappraisal.relationship_health=%.2f", indent, appraisal.RelationshipHealth),
		)
	}
	if len(appraisal.Basis) > 0 {
		lines = append(lines, fmt.Sprintf("%sappraisal.basis=%s", indent, strings.Join(SampleStrings(appraisal.Basis, 6), "; ")))
	}
	return strings.Join(lines, "\n")
}

func BuildActionSelectionContext(selection *model.ActionSelectionState, indent string) string {
	if selection == nil {
		return ""
	}
	lines := []string{
		"Action selection context:",
		fmt.Sprintf("%saction.domain=%s", indent, selection.Domain),
		fmt.Sprintf("%saction.recommended=%s", indent, selection.RecommendedAction),
		fmt.Sprintf("%saction.success_probability=%.2f", indent, selection.SuccessProbability),
		fmt.Sprintf("%saction.goal_value=%.2f", indent, selection.GoalValue),
		fmt.Sprintf("%saction.social_cost=%.2f", indent, selection.SocialCost),
		fmt.Sprintf("%saction.expected_utility=%.2f", indent, selection.ExpectedUtility),
	}
	if selection.RequestedActionMatch > 0 {
		lines = append(lines, fmt.Sprintf("%saction.requested_action_match=%.2f", indent, selection.RequestedActionMatch))
	}
	if len(selection.Basis) > 0 {
		lines = append(lines, fmt.Sprintf("%saction.basis=%s", indent, strings.Join(SampleStrings(selection.Basis, 8), "; ")))
	}
	return strings.Join(lines, "\n")
}

func expectationAction(importance, reliability, tradability float64) string {
	switch {
	case tradability >= 0.68 && reliability >= 0.60:
		return "investigate"
	case importance >= 0.45 || tradability >= 0.40:
		return "monitor"
	default:
		return "ignore"
	}
}

func predictedDirection(sig signal.Signal) string {
	if sig.Direction != "" {
		return string(sig.Direction)
	}
	return "unspecified"
}

func appraisalModerators(sig signal.Signal, expectation *model.ExpectationState, input *model.CollaborationInput) (power, distance, rank float64) {
	if input == nil {
		return clamp01(expectation.PredictedReliability), 0.60, clamp01(sig.Urgency)
	}
	power = clamp01(0.50*input.RelationshipTrust + 0.25*expectation.PredictedImportance + 0.25*expectation.PredictedTradability)
	distance = clamp01(1 - input.RelationshipConfidence)
	rank = clamp01(max3(actionRank(input.RequestedAction), expectation.PredictedTradability, clamp01(sig.Urgency)))
	return power, distance, rank
}

func relationshipHealth(predictedNovelty, power, distance float64) float64 {
	currentTension := clamp01((power*(1-distance) + predictedNovelty) / 2)
	return clamp01(1 - math.Abs(currentTension-0.50))
}

func appraisePressureBonus(actionPressure, pressureWeight float64) float64 {
	return clamp01(actionPressure) * clamp01(pressureWeight)
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

func deriveNovelty(sig signal.Signal) float64 {
	score := 0.45
	if len(sig.CorroboratingLanguages) > 1 {
		score += 0.10
	}
	if len(sig.CorroboratingSources) > 2 {
		score += 0.10
	}
	if sig.NarrativeClusterID != "" {
		score += 0.10
	}
	if len(sig.RelatedSignalIDs) == 0 {
		score += 0.05
	}
	return clamp01(score)
}

func evidenceTrust(meta *evidence.Metadata) float64 {
	if meta == nil || meta.SourceTrust <= 0 {
		return 0.50
	}
	return clamp01(meta.SourceTrust)
}

func evidenceScore(meta *evidence.Metadata) float64 {
	if meta == nil || meta.EvidenceScore <= 0 {
		return 0.50
	}
	return clamp01(meta.EvidenceScore)
}

func freshnessScore(meta *evidence.Metadata) float64 {
	if meta == nil {
		return 0.50
	}
	switch strings.ToLower(strings.TrimSpace(meta.FreshnessStatus)) {
	case "fresh":
		return 1.00
	case "recent":
		return 0.80
	case "aging":
		return 0.60
	case "stale":
		return 0.35
	case "expired":
		return 0.15
	default:
		if meta.FreshnessWindowHours > 0 && meta.FreshnessAgeHours >= 0 {
			return clamp01(1 - (meta.FreshnessAgeHours / meta.FreshnessWindowHours))
		}
		return 0.50
	}
}

func leadTimeScore(meta *evidence.Metadata) float64 {
	if meta == nil {
		return 0.40
	}
	if meta.LeadTimeScore > 0 {
		return clamp01(meta.LeadTimeScore)
	}
	if meta.LeadTimeAverageHours > 0 && meta.LeadTimeObservations > 0 {
		return clamp01((meta.LeadTimeAverageHours / 6.0) * math.Min(float64(meta.LeadTimeObservations)/5.0, 1.0))
	}
	return 0.40
}

func confidenceScore(meta *evidence.Metadata, selector func(*evidence.ConfidenceVector) float64, fallback float64) float64 {
	if meta == nil || meta.ConfidenceVector == nil {
		return clamp01(fallback)
	}
	value := selector(meta.ConfidenceVector)
	if value <= 0 {
		return clamp01(fallback)
	}
	return clamp01(value)
}

func actionRank(action string) float64 {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "escalate":
		return 0.95
	case "review":
		return 0.75
	case "monitor":
		return 0.55
	default:
		return 0.35
	}
}

func clamp01(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}

func max3(a, b, c float64) float64 {
	return math.Max(a, math.Max(b, c))
}
