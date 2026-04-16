package firm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed factor_policy.json
var embeddedFactorPolicy []byte

type factorPolicy struct {
	AlertGrossExposurePct    float64      `json:"alert_gross_exposure_pct"`
	AlertNetExposurePct      float64      `json:"alert_net_exposure_pct"`
	HiddenOverlapDeskCount   int          `json:"hidden_overlap_desk_count"`
	HistoryLookbackSnapshots int          `json:"history_lookback_snapshots"`
	HistoryFloorSnapshots    int          `json:"history_floor_snapshots"`
	HistoryPenalty           float64      `json:"history_penalty"`
	ReallocationPenalty      float64      `json:"reallocation_penalty"`
	MinWeightFloor           float64      `json:"min_weight_floor"`
	Rules                    []factorRule `json:"rules"`
}

type factorRule struct {
	ID         string   `json:"id"`
	Domains    []string `json:"domains,omitempty"`
	SecTypes   []string `json:"sec_types,omitempty"`
	Symbols    []string `json:"symbols,omitempty"`
	Structures []string `json:"structures,omitempty"`
	Currencies []string `json:"currencies,omitempty"`
	Weight     float64  `json:"weight,omitempty"`
}

var (
	factorPolicyOnce sync.Once
	factorPolicyData factorPolicy
	factorPolicyErr  error
)

func activeFactorPolicy() factorPolicy {
	factorPolicyOnce.Do(func() {
		factorPolicyData, factorPolicyErr = loadFactorPolicy()
		if factorPolicyErr != nil {
			panic(factorPolicyErr)
		}
	})
	return factorPolicyData
}

func loadFactorPolicy() (factorPolicy, error) {
	path := strings.TrimSpace(os.Getenv("FACTOR_POLICY_FILE"))
	raw := embeddedFactorPolicy
	if path != "" {
		loaded, err := os.ReadFile(path)
		if err != nil {
			return factorPolicy{}, fmt.Errorf("read factor policy %s: %w", path, err)
		}
		raw = loaded
	}

	var policy factorPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return factorPolicy{}, fmt.Errorf("decode factor policy: %w", err)
	}
	if err := validateFactorPolicy(policy); err != nil {
		return factorPolicy{}, err
	}
	return normalizeFactorPolicy(policy), nil
}

func validateFactorPolicy(policy factorPolicy) error {
	switch {
	case policy.AlertGrossExposurePct <= 0:
		return fmt.Errorf("factor policy alert_gross_exposure_pct must be positive")
	case policy.AlertNetExposurePct <= 0:
		return fmt.Errorf("factor policy alert_net_exposure_pct must be positive")
	case policy.HiddenOverlapDeskCount <= 0:
		return fmt.Errorf("factor policy hidden_overlap_desk_count must be positive")
	case policy.HistoryLookbackSnapshots <= 0:
		return fmt.Errorf("factor policy history_lookback_snapshots must be positive")
	case policy.HistoryFloorSnapshots <= 0:
		return fmt.Errorf("factor policy history_floor_snapshots must be positive")
	case policy.HistoryPenalty < 0 || policy.HistoryPenalty > 1:
		return fmt.Errorf("factor policy history_penalty must be within [0,1]")
	case policy.ReallocationPenalty < 0 || policy.ReallocationPenalty > 1:
		return fmt.Errorf("factor policy reallocation_penalty must be within [0,1]")
	case policy.MinWeightFloor <= 0 || policy.MinWeightFloor > 1:
		return fmt.Errorf("factor policy min_weight_floor must be within (0,1]")
	case len(policy.Rules) == 0:
		return fmt.Errorf("factor policy must define at least one rule")
	}
	for i, rule := range policy.Rules {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("factor policy rule %d has empty id", i)
		}
		if rule.Weight == 0 {
			return fmt.Errorf("factor policy rule %s has zero weight", rule.ID)
		}
	}
	return nil
}

func normalizeFactorPolicy(policy factorPolicy) factorPolicy {
	for i := range policy.Rules {
		policy.Rules[i].ID = strings.TrimSpace(policy.Rules[i].ID)
		policy.Rules[i].Weight = absOrOne(policy.Rules[i].Weight)
		policy.Rules[i].Domains = normalizeLower(policy.Rules[i].Domains)
		policy.Rules[i].SecTypes = normalizeUpper(policy.Rules[i].SecTypes)
		policy.Rules[i].Symbols = normalizeUpper(policy.Rules[i].Symbols)
		policy.Rules[i].Structures = normalizeLower(policy.Rules[i].Structures)
		policy.Rules[i].Currencies = normalizeUpper(policy.Rules[i].Currencies)
	}
	return policy
}

func normalizeLower(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizeUpper(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func absOrOne(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
