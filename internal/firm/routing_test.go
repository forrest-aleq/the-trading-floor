package firm

import (
	"encoding/json"
	"testing"

	"github.com/hnic/trading-floor/pkg/evidence"
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
	geo := signal.Signal{
		Type:     signal.TypeNews,
		Category: "geopolitical",
		EvidenceMeta: &evidence.Metadata{
			SourceType:   "secondary",
			OriginRegion: "europe",
		},
	}
	if !domainShouldReviewSignal("geopolitical", geo) {
		t.Fatal("expected geopolitical desk to receive geopolitical signal")
	}
	if !domainShouldReviewSignal("macro", geo) {
		t.Fatal("expected macro desk to receive geopolitical signal")
	}
	if domainShouldReviewSignal("corporate", geo) {
		t.Fatal("did not expect corporate desk to receive geopolitical signal")
	}

	macro := signal.Signal{
		Type: signal.TypeEconomic,
		EvidenceMeta: &evidence.Metadata{
			SourceType:       "primary",
			SourceOwnerGroup: "federal_reserve",
		},
	}
	if !domainShouldReviewSignal("macro", macro) {
		t.Fatal("expected macro desk to receive macro primary signal")
	}
	if !domainShouldReviewSignal("systematic", macro) {
		t.Fatal("expected systematic desk to receive macro primary signal")
	}
	if domainShouldReviewSignal("corporate", macro) {
		t.Fatal("did not expect corporate desk to receive macro primary signal")
	}

	corp := signal.Signal{
		Type:     signal.TypeNews,
		Category: "corporate",
		EvidenceMeta: &evidence.Metadata{
			SourceOwnerGroup: "earnings_provider",
			SourceType:       "secondary",
		},
	}
	if !domainShouldReviewSignal("corporate", corp) {
		t.Fatal("expected corporate desk to receive earnings signal")
	}
	if !domainShouldReviewSignal("sector", corp) {
		t.Fatal("expected sector desk to receive earnings signal")
	}
	if domainShouldReviewSignal("geopolitical", corp) {
		t.Fatal("did not expect geopolitical desk to receive earnings signal")
	}

	flow := signal.Signal{
		Type: signal.TypeSocial,
		EvidenceMeta: &evidence.Metadata{
			SourceType: "social",
		},
	}
	if !domainShouldReviewSignal("flows", flow) {
		t.Fatal("expected flows desk to receive social signal")
	}
	if !domainShouldReviewSignal("volatility", flow) {
		t.Fatal("expected volatility desk to receive social signal")
	}
	if domainShouldReviewSignal("macro", flow) {
		t.Fatal("did not expect macro desk to receive social signal")
	}
}

func TestParseRoutingPolicyNormalizesKeysAndDomains(t *testing.T) {
	policy, err := parseRoutingPolicy([]byte(`{
		"category_domain_rules": {" Corporate ": [" Sector ", "corporate", "sector"]},
		"signal_type_domain_rules": {" NEWS ": ["macro"]},
		"fallback_domains": [" Macro ", "systematic", "macro"]
	}`))
	if err != nil {
		t.Fatalf("parse routing policy: %v", err)
	}

	got := policy.CategoryDomainRules["corporate"]
	if len(got) != 2 || got[0] != "sector" || got[1] != "corporate" {
		t.Fatalf("unexpected normalized category rule: %#v", got)
	}
	if len(policy.SignalTypeDomainRules["news"]) != 1 || policy.SignalTypeDomainRules["news"][0] != "macro" {
		t.Fatalf("unexpected normalized signal-type rule: %#v", policy.SignalTypeDomainRules["news"])
	}
	if len(policy.FallbackDomains) != 2 || policy.FallbackDomains[0] != "macro" || policy.FallbackDomains[1] != "systematic" {
		t.Fatalf("unexpected fallback domains: %#v", policy.FallbackDomains)
	}
}
