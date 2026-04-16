package firm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed collaboration_policy.json
var embeddedCollaborationPolicy []byte

type collaborationPolicy struct {
	MinPublishConviction float64             `json:"min_publish_conviction"`
	MaxInternalDepth     int                 `json:"max_internal_depth"`
	ProposalAction       string              `json:"proposal_action"`
	ReplyAction          string              `json:"reply_action"`
	DownstreamDomains    map[string][]string `json:"downstream_domains"`
}

var (
	collaborationPolicyOnce   sync.Once
	cachedCollaborationPolicy collaborationPolicy
)

func activeCollaborationPolicy() collaborationPolicy {
	collaborationPolicyOnce.Do(func() {
		policy, err := loadCollaborationPolicy()
		if err != nil {
			cachedCollaborationPolicy = defaultCollaborationPolicy()
			return
		}
		cachedCollaborationPolicy = policy
	})
	return cachedCollaborationPolicy
}

func loadCollaborationPolicy() (collaborationPolicy, error) {
	raw := embeddedCollaborationPolicy
	if path := strings.TrimSpace(os.Getenv("FIRM_COLLABORATION_POLICY_PATH")); path != "" {
		loaded, err := os.ReadFile(path)
		if err != nil {
			return collaborationPolicy{}, fmt.Errorf("read collaboration policy %s: %w", path, err)
		}
		raw = loaded
	}

	var policy collaborationPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return collaborationPolicy{}, fmt.Errorf("decode collaboration policy: %w", err)
	}
	policy.MinPublishConviction = clampCollaborationUnit(policy.MinPublishConviction, 0.72)
	if policy.MaxInternalDepth <= 0 {
		policy.MaxInternalDepth = 2
	}
	policy.ProposalAction = normalizeCollaborationAction(policy.ProposalAction, "review")
	policy.ReplyAction = normalizeCollaborationAction(policy.ReplyAction, "synthesize")
	policy.DownstreamDomains = normalizeCollaborationRuleMap(policy.DownstreamDomains)
	return policy, nil
}

func defaultCollaborationPolicy() collaborationPolicy {
	return collaborationPolicy{
		MinPublishConviction: 0.72,
		MaxInternalDepth:     2,
		ProposalAction:       "review",
		ReplyAction:          "synthesize",
		DownstreamDomains: map[string][]string{
			"geopolitical": {"macro", "tail", "volatility", "sector"},
			"macro":        {"volatility", "tail", "systematic", "sector"},
			"corporate":    {"sector", "flows", "volatility"},
			"flows":        {"volatility", "systematic", "sector"},
			"tail":         {"macro", "volatility", "geopolitical", "systematic"},
			"volatility":   {"flows", "tail", "systematic"},
			"sector":       {"corporate", "flows", "systematic"},
			"systematic":   {"flows", "volatility", "macro", "sector"},
		},
	}
}

func normalizeCollaborationRuleMap(input map[string][]string) map[string][]string {
	if len(input) == 0 {
		return defaultCollaborationPolicy().DownstreamDomains
	}
	normalized := make(map[string][]string, len(input))
	for key, values := range input {
		normalized[strings.TrimSpace(strings.ToLower(key))] = normalizeDomains(values)
	}
	return normalized
}

func normalizeCollaborationAction(value, fallback string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	return value
}

func clampCollaborationUnit(value, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
