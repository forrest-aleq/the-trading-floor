package firm

import (
	"encoding/json"
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

func TestInternalSignalsRouteUsingExplicitTargetDomains(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"origin_desk":    "geo-cascade-a",
		"target_domains": []string{"macro", "tail", "volatility"},
		"internal_depth": 1,
	})
	internal := signal.Signal{
		Source:   "internal/geo-cascade-a",
		Type:     signal.TypeAlternative,
		Category: "geopolitical",
		Raw:      raw,
	}

	if !domainShouldReviewSignal("macro", internal) {
		t.Fatal("expected macro desk to receive internal thesis signal")
	}
	if !domainShouldReviewSignal("tail", internal) {
		t.Fatal("expected tail desk to receive internal thesis signal")
	}
	if domainShouldReviewSignal("corporate", internal) {
		t.Fatal("did not expect corporate desk to receive unrelated internal thesis signal")
	}
}

func TestSourceDrivenRoutingSpecializesDeskIntake(t *testing.T) {
	geo := signal.Signal{Source: "ft-world", Type: signal.TypeNews}
	if !domainShouldReviewSignal("geopolitical", geo) {
		t.Fatal("expected geopolitical desk to receive ft-world signal")
	}
	if !domainShouldReviewSignal("macro", geo) {
		t.Fatal("expected macro desk to receive ft-world signal")
	}
	if domainShouldReviewSignal("corporate", geo) {
		t.Fatal("did not expect corporate desk to receive ft-world signal")
	}

	macro := signal.Signal{Source: "fred", Type: signal.TypeEconomic}
	if !domainShouldReviewSignal("macro", macro) {
		t.Fatal("expected macro desk to receive FRED signal")
	}
	if !domainShouldReviewSignal("systematic", macro) {
		t.Fatal("expected systematic desk to receive FRED signal")
	}
	if domainShouldReviewSignal("corporate", macro) {
		t.Fatal("did not expect corporate desk to receive FRED signal")
	}

	corp := signal.Signal{Source: "earnings-calendar", Type: signal.TypeNews}
	if !domainShouldReviewSignal("corporate", corp) {
		t.Fatal("expected corporate desk to receive earnings signal")
	}
	if !domainShouldReviewSignal("sector", corp) {
		t.Fatal("expected sector desk to receive earnings signal")
	}
	if domainShouldReviewSignal("geopolitical", corp) {
		t.Fatal("did not expect geopolitical desk to receive earnings signal")
	}

	flow := signal.Signal{Source: "stocktwits", Type: signal.TypeSocial}
	if !domainShouldReviewSignal("flows", flow) {
		t.Fatal("expected flows desk to receive stocktwits signal")
	}
	if !domainShouldReviewSignal("volatility", flow) {
		t.Fatal("expected volatility desk to receive stocktwits signal")
	}
	if domainShouldReviewSignal("macro", flow) {
		t.Fatal("did not expect macro desk to receive stocktwits signal")
	}
}
