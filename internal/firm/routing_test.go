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
		"domain_group_max_matches": {" Corporate ": 2, " macro ": 0},
		"desk_rules": {
			" Corp-Earnings-A ": {
				"priority": 12,
				"categories": [" Corporate "],
				"keywords_any": [" Earnings ", "guidance", "earnings"]
			}
		},
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
	if policy.DomainGroupMaxMatches["corporate"] != 2 {
		t.Fatalf("unexpected domain group max matches: %#v", policy.DomainGroupMaxMatches)
	}
	rule, ok := policy.DeskRules["corp-earnings-a"]
	if !ok {
		t.Fatalf("expected normalized desk rule map: %#v", policy.DeskRules)
	}
	if rule.Priority != 12 {
		t.Fatalf("unexpected desk rule priority: %#v", rule)
	}
	if len(rule.KeywordsAny) != 2 || rule.KeywordsAny[0] != "earnings" || rule.KeywordsAny[1] != "guidance" {
		t.Fatalf("unexpected normalized keyword list: %#v", rule.KeywordsAny)
	}
}

func TestShouldDeskReviewSignalUsesDeskSpecificRules(t *testing.T) {
	sig := signal.Signal{
		Source:   "earnings-calendar",
		Type:     signal.TypeNews,
		Category: "corporate",
		Entities: []signal.Entity{
			{Name: "NVIDIA", Type: "company", ID: "NVDA"},
			{Name: "NVDA", Type: "instrument", ID: "NVDA"},
		},
		Translated: "NVIDIA beat earnings, raised guidance, and expanded data center backlog.",
		EvidenceMeta: &evidence.Metadata{
			SourceOwnerGroup: "earnings_provider",
		},
	}

	if !ShouldDeskReviewSignal("corp-earnings-a", "corporate", sig) {
		t.Fatal("expected corp-earnings-a to receive earnings signal")
	}
	if ShouldDeskReviewSignal("corp-filings-a", "corporate", sig) {
		t.Fatal("did not expect corp-filings-a to receive earnings signal")
	}
	if ShouldDeskReviewSignal("corp-mna-a", "corporate", sig) {
		t.Fatal("did not expect corp-mna-a to receive earnings signal")
	}
	if !ShouldDeskReviewSignal("sector-tech-a", "sector", sig) {
		t.Fatal("expected sector-tech-a to receive tech earnings signal")
	}
	if ShouldDeskReviewSignal("sector-biotech-a", "sector", sig) {
		t.Fatal("did not expect sector-biotech-a to receive tech earnings signal")
	}
}

func TestSelectDeskTargetsForSignalAppliesPerDomainGroupLimit(t *testing.T) {
	desks := []*Desk{
		{ID: "corp-a-1", Domain: "corporate", ABGroup: "A"},
		{ID: "corp-a-2", Domain: "corporate", ABGroup: "A"},
		{ID: "corp-b-1", Domain: "corporate", ABGroup: "B"},
		{ID: "corp-b-2", Domain: "corporate", ABGroup: "B"},
	}

	sig := signal.Signal{
		Source:     "generic-corporate",
		Type:       signal.TypeNews,
		Category:   "corporate",
		Translated: "Generic corporate update with no desk-specific rule attached.",
	}

	targets := selectDeskTargetsForSignal(desks, sig)
	if len(targets) != 2 {
		t.Fatalf("expected one desk per AB group after cap, got %d", len(targets))
	}
	got := []string{targets[0].ID, targets[1].ID}
	want := []string{"corp-a-1", "corp-b-1"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets[%d] = %s, want %s (all=%v)", i, got[i], want[i], got)
		}
	}
}
