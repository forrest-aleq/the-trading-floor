package firm

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestDomainShouldReviewSignalUsesDeterministicPrefilter(t *testing.T) {
	corporate := signal.Signal{Category: "corporate", Type: signal.TypeNews}
	if !domainShouldReviewSignal("corporate", corporate) {
		t.Fatal("expected corporate desk to receive corporate signal")
	}
	if !domainShouldReviewSignal("sector", corporate) {
		t.Fatal("expected sector desk to receive corporate signal")
	}
	if domainShouldReviewSignal("geopolitical", corporate) {
		t.Fatal("did not expect geopolitical desk to receive corporate signal")
	}

	flow := signal.Signal{Category: "flows", Type: signal.TypeSocial}
	if !domainShouldReviewSignal("flows", flow) {
		t.Fatal("expected flows desk to receive social flow signal")
	}
	if !domainShouldReviewSignal("volatility", flow) {
		t.Fatal("expected volatility desk to receive social flow signal")
	}
	if domainShouldReviewSignal("corporate", flow) {
		t.Fatal("did not expect corporate desk to receive social flow signal")
	}
}

func TestUnknownSignalsFallBackToBroadReview(t *testing.T) {
	unknown := signal.Signal{Category: "", Type: signal.TypeNews}
	if !domainShouldReviewSignal("macro", unknown) {
		t.Fatal("expected unknown signal to remain eligible for macro review")
	}
	if !domainShouldReviewSignal("systematic", unknown) {
		t.Fatal("expected unknown signal to remain eligible for systematic review")
	}
}
