package institutional

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed action_policy.json
var embeddedActionPolicy []byte

type actionPolicy struct {
	Actions []actionRule `json:"actions"`
}

type actionRule struct {
	Name                 string  `json:"name"`
	BaseGoalValue        float64 `json:"base_goal_value"`
	SocialPenaltyWeight  float64 `json:"social_penalty_weight"`
	ImportanceThreshold  float64 `json:"importance_threshold"`
	ReliabilityThreshold float64 `json:"reliability_threshold"`
	TradabilityThreshold float64 `json:"tradability_threshold"`
	PressureWeight       float64 `json:"pressure_weight"`
	PeerWeight           float64 `json:"peer_weight"`
}

var (
	actionPolicyOnce   sync.Once
	cachedActionPolicy actionPolicy
)

func activeActionPolicy() actionPolicy {
	actionPolicyOnce.Do(func() {
		policy, err := loadActionPolicy()
		if err != nil {
			cachedActionPolicy = defaultActionPolicy()
			return
		}
		cachedActionPolicy = policy
	})
	return cachedActionPolicy
}

func loadActionPolicy() (actionPolicy, error) {
	raw := embeddedActionPolicy
	if path := strings.TrimSpace(os.Getenv("INSTITUTIONAL_ACTION_POLICY_PATH")); path != "" {
		loaded, err := os.ReadFile(path)
		if err != nil {
			return actionPolicy{}, fmt.Errorf("read action policy %s: %w", path, err)
		}
		raw = loaded
	}

	var policy actionPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return actionPolicy{}, fmt.Errorf("decode action policy: %w", err)
	}
	if len(policy.Actions) == 0 {
		return actionPolicy{}, fmt.Errorf("action policy must define at least one action")
	}
	for i := range policy.Actions {
		policy.Actions[i].Name = strings.TrimSpace(strings.ToLower(policy.Actions[i].Name))
		policy.Actions[i].BaseGoalValue = clamp01(policy.Actions[i].BaseGoalValue)
		policy.Actions[i].SocialPenaltyWeight = clamp01(policy.Actions[i].SocialPenaltyWeight)
		policy.Actions[i].ImportanceThreshold = clamp01(policy.Actions[i].ImportanceThreshold)
		policy.Actions[i].ReliabilityThreshold = clamp01(policy.Actions[i].ReliabilityThreshold)
		policy.Actions[i].TradabilityThreshold = clamp01(policy.Actions[i].TradabilityThreshold)
		policy.Actions[i].PressureWeight = clamp01(policy.Actions[i].PressureWeight)
		policy.Actions[i].PeerWeight = clamp01(policy.Actions[i].PeerWeight)
		if policy.Actions[i].Name == "" {
			return actionPolicy{}, fmt.Errorf("action policy has empty action name")
		}
	}
	return policy, nil
}

func defaultActionPolicy() actionPolicy {
	return actionPolicy{
		Actions: []actionRule{
			{Name: "ignore", BaseGoalValue: 0.25, SocialPenaltyWeight: 0.10, PressureWeight: 0.10},
			{Name: "monitor", BaseGoalValue: 0.45, SocialPenaltyWeight: 0.20, ImportanceThreshold: 0.25, ReliabilityThreshold: 0.25, PressureWeight: 0.30, PeerWeight: 0.15},
			{Name: "investigate", BaseGoalValue: 0.70, SocialPenaltyWeight: 0.35, ImportanceThreshold: 0.45, ReliabilityThreshold: 0.45, TradabilityThreshold: 0.30, PressureWeight: 0.45, PeerWeight: 0.20},
			{Name: "escalate", BaseGoalValue: 0.88, SocialPenaltyWeight: 0.55, ImportanceThreshold: 0.65, ReliabilityThreshold: 0.55, TradabilityThreshold: 0.45, PressureWeight: 0.60, PeerWeight: 0.25},
		},
	}
}
