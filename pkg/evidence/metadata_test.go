package evidence

import (
	"math"
	"testing"
)

func TestDeterministicGateRejectsLowConfidenceDimensions(t *testing.T) {
	meta := &Metadata{
		SourceTrust:   0.85,
		SourceType:    "primary",
		EvidenceScore: 0.52,
		ConfidenceVector: &ConfidenceVector{
			FactConfidence:          0.72,
			NoveltyConfidence:       0.61,
			MarketMappingConfidence: 0.18,
			ExpressionConfidence:    0.54,
			ExecutionConfidence:     0.58,
			CompetenceConfidence:    0.62,
		},
	}

	allowed, reason := meta.DeterministicGate()
	if allowed {
		t.Fatal("expected low market mapping confidence to be rejected")
	}
	if reason != "low_market_mapping_confidence" {
		t.Fatalf("unexpected reject reason: %s", reason)
	}
}

func TestConfidenceVectorOverall(t *testing.T) {
	vector := &ConfidenceVector{
		FactConfidence:          0.6,
		NoveltyConfidence:       0.5,
		MarketMappingConfidence: 0.4,
		ExpressionConfidence:    0.3,
		ExecutionConfidence:     0.2,
		CompetenceConfidence:    0.1,
	}
	if got := vector.Overall(); math.Abs(got-0.35) > 1e-9 {
		t.Fatalf("unexpected overall score: %.2f", got)
	}
}
