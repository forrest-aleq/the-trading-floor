package firm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed routing_policy.json
var embeddedRoutingPolicy []byte

type routingPolicy struct {
	CategoryDomainRules   map[string][]string `json:"category_domain_rules"`
	SignalTypeDomainRules map[string][]string `json:"signal_type_domain_rules"`
	SourceTypeDomainRules map[string][]string `json:"source_type_domain_rules"`
	OwnerGroupDomainRules map[string][]string `json:"owner_group_domain_rules"`
	FallbackDomains       []string            `json:"fallback_domains"`
}

var (
	routingPolicyOnce sync.Once
	routingPolicyData routingPolicy
	routingPolicyErr  error
)

func activeRoutingPolicy() routingPolicy {
	routingPolicyOnce.Do(func() {
		routingPolicyData, routingPolicyErr = loadRoutingPolicy()
		if routingPolicyErr != nil {
			panic(routingPolicyErr)
		}
	})
	return routingPolicyData
}

func loadRoutingPolicy() (routingPolicy, error) {
	path := strings.TrimSpace(os.Getenv("DESK_ROUTING_POLICY_FILE"))
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return routingPolicy{}, fmt.Errorf("read routing policy %s: %w", path, err)
		}
		return parseRoutingPolicy(raw)
	}
	return parseRoutingPolicy(embeddedRoutingPolicy)
}

func parseRoutingPolicy(raw []byte) (routingPolicy, error) {
	var policy routingPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return routingPolicy{}, fmt.Errorf("decode routing policy: %w", err)
	}
	policy.CategoryDomainRules = normalizeRuleMap(policy.CategoryDomainRules)
	policy.SignalTypeDomainRules = normalizeRuleMap(policy.SignalTypeDomainRules)
	policy.SourceTypeDomainRules = normalizeRuleMap(policy.SourceTypeDomainRules)
	policy.OwnerGroupDomainRules = normalizeRuleMap(policy.OwnerGroupDomainRules)
	policy.FallbackDomains = normalizeDomains(policy.FallbackDomains)
	return policy, nil
}

func normalizeRuleMap(input map[string][]string) map[string][]string {
	if len(input) == 0 {
		return map[string][]string{}
	}
	normalized := make(map[string][]string, len(input))
	for key, values := range input {
		key = strings.TrimSpace(strings.ToLower(key))
		if key == "" {
			continue
		}
		normalized[key] = normalizeDomains(values)
	}
	return normalized
}

func normalizeDomains(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := newDomainSet()
	set.add(values...)
	return set.values()
}
