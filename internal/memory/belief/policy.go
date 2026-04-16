package belief

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed ceiling_policy.json
var embeddedCeilingPolicy []byte

type ceilingBand struct {
	MaxValidatedOutcomes int     `json:"max_validated_outcomes"`
	TrustCeiling         float64 `json:"trust_ceiling"`
	ConfidenceCeiling    float64 `json:"confidence_ceiling"`
}

var (
	ceilingPolicyOnce sync.Once
	ceilingPolicyData []ceilingBand
	ceilingPolicyErr  error
)

func activeCeilingPolicy() []ceilingBand {
	ceilingPolicyOnce.Do(func() {
		ceilingPolicyData, ceilingPolicyErr = loadCeilingPolicy()
		if ceilingPolicyErr != nil {
			panic(ceilingPolicyErr)
		}
	})
	return ceilingPolicyData
}

func loadCeilingPolicy() ([]ceilingBand, error) {
	path := strings.TrimSpace(os.Getenv("BELIEF_CEILING_POLICY_FILE"))
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read belief ceiling policy %s: %w", path, err)
		}
		return parseCeilingPolicy(raw)
	}
	return parseCeilingPolicy(embeddedCeilingPolicy)
}

func parseCeilingPolicy(raw []byte) ([]ceilingBand, error) {
	var bands []ceilingBand
	if err := json.Unmarshal(raw, &bands); err != nil {
		return nil, fmt.Errorf("decode belief ceiling policy: %w", err)
	}
	if len(bands) == 0 {
		return nil, fmt.Errorf("belief ceiling policy must contain at least one band")
	}
	lastMax := -1
	for i, band := range bands {
		switch {
		case band.MaxValidatedOutcomes < 0:
			return nil, fmt.Errorf("belief ceiling policy band %d has negative max_validated_outcomes", i)
		case band.TrustCeiling <= 0 || band.TrustCeiling > 1:
			return nil, fmt.Errorf("belief ceiling policy band %d has invalid trust_ceiling", i)
		case band.ConfidenceCeiling <= 0 || band.ConfidenceCeiling > 1:
			return nil, fmt.Errorf("belief ceiling policy band %d has invalid confidence_ceiling", i)
		case band.MaxValidatedOutcomes < lastMax:
			return nil, fmt.Errorf("belief ceiling policy must be sorted by max_validated_outcomes")
		}
		lastMax = band.MaxValidatedOutcomes
	}
	return bands, nil
}

func ceilingBandForValidatedOutcomes(validated int) ceilingBand {
	if validated < 0 {
		validated = 0
	}
	for _, band := range activeCeilingPolicy() {
		if validated <= band.MaxValidatedOutcomes {
			return band
		}
	}
	return activeCeilingPolicy()[len(activeCeilingPolicy())-1]
}
