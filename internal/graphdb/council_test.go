package graphdb

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestCouncilVoiceContributionScoreRewardsCorrectReject(t *testing.T) {
	voice := model.CouncilVoiceContribution{
		Name:                 "Abstain",
		Recommendation:       model.CouncilAbstain,
		ConvictionAdjustment: -0.12,
		SizeAdjustment:       0.80,
		Weight:               1.2,
	}
	attr := &model.OutcomeAttribution{
		TruthEdge:      -0.9,
		TimingEdge:     -0.7,
		ExpressionEdge: -0.4,
		ExecutionEdge:  -0.2,
	}

	score, counted := councilVoiceContributionScore(voice, attr)
	if !counted {
		t.Fatal("expected contribution to be counted")
	}
	if score <= 0 {
		t.Fatalf("expected positive score for correct abstain, got %.2f", score)
	}
}

func TestCouncilVoiceWeightClamps(t *testing.T) {
	if got := councilVoiceWeight(10); got != 1.35 {
		t.Fatalf("expected upper clamp, got %.2f", got)
	}
	if got := councilVoiceWeight(-10); got != 0.75 {
		t.Fatalf("expected lower clamp, got %.2f", got)
	}
}
