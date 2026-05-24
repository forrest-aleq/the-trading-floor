package risk

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed limits.json
var embeddedLimits []byte

var (
	limitsOnce sync.Once
	limitsData Limits
	limitsErr  error
)

func loadActiveLimits() Limits {
	limitsOnce.Do(func() {
		limitsData, limitsErr = loadLimits()
		if limitsErr != nil {
			panic(limitsErr)
		}
	})
	return limitsData
}

func loadLimits() (Limits, error) {
	path := strings.TrimSpace(os.Getenv("RISK_LIMITS_FILE"))
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Limits{}, fmt.Errorf("read risk limits %s: %w", path, err)
		}
		return parseLimits(raw)
	}
	return parseLimits(embeddedLimits)
}

func parseLimits(raw []byte) (Limits, error) {
	var limits Limits
	if err := json.Unmarshal(raw, &limits); err != nil {
		return Limits{}, fmt.Errorf("decode risk limits: %w", err)
	}
	if err := validateLimits(limits); err != nil {
		return Limits{}, err
	}
	return limits, nil
}

func validateLimits(limits Limits) error {
	switch {
	case limits.MaxDailyLossPct <= 0:
		return fmt.Errorf("max_daily_loss_pct must be positive")
	case limits.MaxSinglePositionPct <= 0:
		return fmt.Errorf("max_single_position_pct must be positive")
	case limits.MaxOpenPositions <= 0:
		return fmt.Errorf("max_open_positions must be positive")
	case limits.CapitalPerDesk <= 0:
		return fmt.Errorf("capital_per_desk must be positive")
	case limits.MaxPositionSizePct <= 0:
		return fmt.Errorf("max_position_size_pct must be positive")
	case limits.MaxPositionSizePct > limits.MaxSinglePositionPct:
		return fmt.Errorf("max_position_size_pct must not exceed max_single_position_pct")
	case limits.MinConvictionScore <= 0 || limits.MinConvictionScore > 1:
		return fmt.Errorf("min_conviction_score must be within (0,1]")
	case limits.MaxQuoteAgeSeconds < 0:
		return fmt.Errorf("max_quote_age_seconds must be non-negative")
	case limits.MaxEquitySpreadBps < 0:
		return fmt.Errorf("max_equity_spread_bps must be non-negative")
	case limits.MaxOptionSpreadBps < 0:
		return fmt.Errorf("max_option_spread_bps must be non-negative")
	case limits.TotalCapital <= 0:
		return fmt.Errorf("total_capital must be positive")
	case limits.MaxGrossExposurePct <= 0:
		return fmt.Errorf("max_gross_exposure_pct must be positive")
	case limits.MaxCashDeployPct <= 0:
		return fmt.Errorf("max_cash_deploy_pct must be positive")
	default:
		return nil
	}
}
