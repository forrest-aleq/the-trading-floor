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
	DomainGroupMaxMatches map[string]int      `json:"domain_group_max_matches"`
	DeskRules             map[string]deskRule `json:"desk_rules"`
	FallbackDomains       []string            `json:"fallback_domains"`
}

type deskRule struct {
	Priority          int      `json:"priority"`
	Categories        []string `json:"categories"`
	SignalTypes       []string `json:"signal_types"`
	SourcePrefixes    []string `json:"source_prefixes"`
	SourceTypes       []string `json:"source_types"`
	SourceOwnerGroups []string `json:"source_owner_groups"`
	OriginRegions     []string `json:"origin_regions"`
	EntityTypes       []string `json:"entity_types"`
	EntityIDs         []string `json:"entity_ids"`
	EntityNames       []string `json:"entity_names"`
	KeywordsAny       []string `json:"keywords_any"`
	KeywordsAll       []string `json:"keywords_all"`
	ExcludeKeywords   []string `json:"exclude_keywords"`
	MinUrgency        float64  `json:"min_urgency"`
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
	policy.DomainGroupMaxMatches = normalizeIntMap(policy.DomainGroupMaxMatches)
	policy.DeskRules = normalizeDeskRules(policy.DeskRules)
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

func normalizeIntMap(input map[string]int) map[string]int {
	if len(input) == 0 {
		return map[string]int{}
	}
	normalized := make(map[string]int, len(input))
	for key, value := range input {
		key = strings.TrimSpace(strings.ToLower(key))
		if key == "" || value <= 0 {
			continue
		}
		normalized[key] = value
	}
	return normalized
}

func normalizeDeskRules(input map[string]deskRule) map[string]deskRule {
	if len(input) == 0 {
		return map[string]deskRule{}
	}
	normalized := make(map[string]deskRule, len(input))
	for key, rule := range input {
		key = strings.TrimSpace(strings.ToLower(key))
		if key == "" {
			continue
		}
		rule.Categories = normalizeStringSlice(rule.Categories)
		rule.SignalTypes = normalizeStringSlice(rule.SignalTypes)
		rule.SourcePrefixes = normalizeStringSlice(rule.SourcePrefixes)
		rule.SourceTypes = normalizeStringSlice(rule.SourceTypes)
		rule.SourceOwnerGroups = normalizeStringSlice(rule.SourceOwnerGroups)
		rule.OriginRegions = normalizeStringSlice(rule.OriginRegions)
		rule.EntityTypes = normalizeStringSlice(rule.EntityTypes)
		rule.EntityIDs = normalizeStringSlice(rule.EntityIDs)
		rule.EntityNames = normalizeStringSlice(rule.EntityNames)
		rule.KeywordsAny = normalizeStringSlice(rule.KeywordsAny)
		rule.KeywordsAll = normalizeStringSlice(rule.KeywordsAll)
		rule.ExcludeKeywords = normalizeStringSlice(rule.ExcludeKeywords)
		if rule.Priority < 0 {
			rule.Priority = 0
		}
		if rule.MinUrgency < 0 {
			rule.MinUrgency = 0
		}
		if rule.MinUrgency > 1 {
			rule.MinUrgency = 1
		}
		normalized[key] = rule
	}
	return normalized
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}
