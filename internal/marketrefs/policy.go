package marketrefs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/hnic/trading-floor/pkg/model"
)

//go:embed policy.json
var embeddedPolicy []byte

type Policy struct {
	MarketSignalWatchlist   []model.Instrument `json:"market_signal_watchlist"`
	StartupPricingWatchlist []model.Instrument `json:"startup_pricing_watchlist"`
	EarningsWatchlist       []model.Instrument `json:"earnings_watchlist"`
	RegimeInstruments       RegimeInstruments  `json:"regime_instruments"`
}

type RegimeInstruments struct {
	Volatility model.Instrument `json:"volatility"`
	Trend      model.Instrument `json:"trend"`
	Risk       model.Instrument `json:"risk"`
}

var (
	policyOnce sync.Once
	policyData Policy
	policyErr  error
)

func ActivePolicy() Policy {
	policyOnce.Do(func() {
		policyData, policyErr = loadPolicy()
		if policyErr != nil {
			panic(policyErr)
		}
	})
	return clonePolicy(policyData)
}

func MarketSignalWatchlist() []model.Instrument {
	return cloneInstruments(ActivePolicy().MarketSignalWatchlist)
}

func StartupPricingWatchlist() []model.Instrument {
	return cloneInstruments(ActivePolicy().StartupPricingWatchlist)
}

func EarningsWatchlist() []model.Instrument {
	return cloneInstruments(ActivePolicy().EarningsWatchlist)
}

func ActiveRegimeInstruments() RegimeInstruments {
	return ActivePolicy().RegimeInstruments
}

func loadPolicy() (Policy, error) {
	raw := embeddedPolicy
	if path := strings.TrimSpace(os.Getenv("MARKET_REFS_POLICY_FILE")); path != "" {
		loaded, err := os.ReadFile(path)
		if err != nil {
			return Policy{}, fmt.Errorf("read market refs policy %s: %w", path, err)
		}
		raw = loaded
	}
	return parsePolicy(raw)
}

func parsePolicy(raw []byte) (Policy, error) {
	var policy Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return Policy{}, fmt.Errorf("decode market refs policy: %w", err)
	}
	if err := validatePolicy(policy); err != nil {
		return Policy{}, err
	}
	return normalizePolicy(policy), nil
}

func validatePolicy(policy Policy) error {
	if len(policy.EarningsWatchlist) == 0 {
		return fmt.Errorf("market refs policy must define at least one earnings instrument")
	}
	for i, inst := range policy.MarketSignalWatchlist {
		if strings.TrimSpace(inst.Symbol) == "" {
			return fmt.Errorf("market signal watchlist instrument %d has empty symbol", i)
		}
	}
	for i, inst := range policy.StartupPricingWatchlist {
		if strings.TrimSpace(inst.Symbol) == "" {
			return fmt.Errorf("startup pricing watchlist instrument %d has empty symbol", i)
		}
	}
	for i, inst := range policy.EarningsWatchlist {
		if strings.TrimSpace(inst.Symbol) == "" {
			return fmt.Errorf("earnings watchlist instrument %d has empty symbol", i)
		}
	}
	switch {
	case strings.TrimSpace(policy.RegimeInstruments.Volatility.Symbol) == "":
		return fmt.Errorf("regime volatility instrument must define a symbol")
	case strings.TrimSpace(policy.RegimeInstruments.Trend.Symbol) == "":
		return fmt.Errorf("regime trend instrument must define a symbol")
	case strings.TrimSpace(policy.RegimeInstruments.Risk.Symbol) == "":
		return fmt.Errorf("regime risk instrument must define a symbol")
	default:
		return nil
	}
}

func normalizePolicy(policy Policy) Policy {
	policy.MarketSignalWatchlist = normalizeInstruments(policy.MarketSignalWatchlist)
	policy.StartupPricingWatchlist = normalizeInstruments(policy.StartupPricingWatchlist)
	policy.EarningsWatchlist = normalizeInstruments(policy.EarningsWatchlist)
	policy.RegimeInstruments = RegimeInstruments{
		Volatility: normalizeInstrument(policy.RegimeInstruments.Volatility),
		Trend:      normalizeInstrument(policy.RegimeInstruments.Trend),
		Risk:       normalizeInstrument(policy.RegimeInstruments.Risk),
	}
	return policy
}

func normalizeInstruments(instruments []model.Instrument) []model.Instrument {
	out := make([]model.Instrument, 0, len(instruments))
	for _, inst := range instruments {
		if normalized := normalizeInstrument(inst); normalized.Symbol != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func normalizeInstrument(inst model.Instrument) model.Instrument {
	inst.Symbol = strings.ToUpper(strings.TrimSpace(inst.Symbol))
	inst.SecType = strings.ToUpper(strings.TrimSpace(inst.SecType))
	inst.Exchange = strings.ToUpper(strings.TrimSpace(inst.Exchange))
	inst.Currency = strings.ToUpper(strings.TrimSpace(inst.Currency))
	return inst
}

func clonePolicy(policy Policy) Policy {
	policy.MarketSignalWatchlist = cloneInstruments(policy.MarketSignalWatchlist)
	policy.StartupPricingWatchlist = cloneInstruments(policy.StartupPricingWatchlist)
	policy.EarningsWatchlist = cloneInstruments(policy.EarningsWatchlist)
	return policy
}

func cloneInstruments(instruments []model.Instrument) []model.Instrument {
	if len(instruments) == 0 {
		return nil
	}
	cloned := make([]model.Instrument, len(instruments))
	copy(cloned, instruments)
	return cloned
}
