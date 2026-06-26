package main

import (
	"fmt"
	"os"
	"strings"
)

type runtimeMode string

const (
	runtimeModeDev            runtimeMode = "dev"
	runtimeModePaper          runtimeMode = "paper"
	runtimeModePaperDiscovery runtimeMode = "paper_discovery"
	runtimeModeLive           runtimeMode = "live"
)

type runtimeReadiness struct {
	Mode                    runtimeMode
	DBReady                 bool
	BrokerExecutionRequired bool
	KalshiExecutionRequired bool
	KalshiExecutionReady    bool
	KalshiDryRun            bool
	BrokerConnected         bool
	BrokerPaper             bool
	MarketStateConfigured   bool
	MarketStateBrokerBacked bool
	StartupPricingReady     bool
	EarningsUniverseReady   bool
	RegimeDetectionEnabled  bool
	RegimeDetectorReady     bool
	RiskTokenConfigured     bool
}

func loadRuntimeMode() (runtimeMode, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("FLOOR_RUNTIME_MODE")))
	if raw == "" {
		return runtimeModeDev, nil
	}
	mode := runtimeMode(raw)
	switch mode {
	case runtimeModeDev, runtimeModePaper, runtimeModePaperDiscovery, runtimeModeLive:
		return mode, nil
	default:
		return "", fmt.Errorf("FLOOR_RUNTIME_MODE must be one of dev|paper|paper_discovery|live")
	}
}

func validateRuntimeReadiness(readiness runtimeReadiness) error {
	switch readiness.Mode {
	case runtimeModeDev:
		return nil
	case runtimeModePaper, runtimeModePaperDiscovery:
		if !readiness.DBReady {
			return fmt.Errorf("%s mode requires PostgreSQL persistence", readiness.Mode)
		}
		if readiness.KalshiExecutionRequired && !readiness.KalshiExecutionReady {
			return fmt.Errorf("%s mode requires Kalshi execution when prediction-market desks are selected", readiness.Mode)
		}
		if readiness.Mode == runtimeModePaperDiscovery && readiness.KalshiExecutionRequired && !readiness.KalshiDryRun {
			return fmt.Errorf("paper_discovery mode requires Kalshi dry-run execution")
		}
		if readiness.BrokerExecutionRequired {
			if !readiness.BrokerConnected {
				return fmt.Errorf("%s mode requires IBKR connectivity at startup", readiness.Mode)
			}
			if !readiness.BrokerPaper {
				return fmt.Errorf("%s mode requires a paper IBKR session", readiness.Mode)
			}
			if !readiness.MarketStateConfigured {
				return fmt.Errorf("%s mode requires an explicit market data provider; TWS is broker/account only", readiness.Mode)
			}
			if readiness.MarketStateBrokerBacked {
				return fmt.Errorf("%s mode requires a non-broker market data provider; TWS is broker/account only", readiness.Mode)
			}
			if !readiness.StartupPricingReady {
				return fmt.Errorf("%s mode requires a non-empty startup pricing watchlist", readiness.Mode)
			}
			if !readiness.RegimeDetectionEnabled {
				return fmt.Errorf("%s mode requires regime detection to be enabled", readiness.Mode)
			}
			if !readiness.RegimeDetectorReady {
				return fmt.Errorf("%s mode requires regime detection to have live market state access", readiness.Mode)
			}
		}
		return nil
	case runtimeModeLive:
		if !readiness.DBReady {
			return fmt.Errorf("live mode requires PostgreSQL persistence")
		}
		if readiness.KalshiExecutionRequired && !readiness.KalshiExecutionReady {
			return fmt.Errorf("live mode requires Kalshi execution when prediction-market desks are selected")
		}
		if readiness.BrokerExecutionRequired {
			if !readiness.BrokerConnected {
				return fmt.Errorf("live mode requires IBKR connectivity at startup")
			}
			if readiness.BrokerPaper {
				return fmt.Errorf("live mode requires a non-paper IBKR session")
			}
			if !readiness.MarketStateConfigured {
				return fmt.Errorf("live mode requires an explicit market data provider; TWS is broker/account only")
			}
			if readiness.MarketStateBrokerBacked {
				return fmt.Errorf("live mode requires a non-broker market data provider; TWS is broker/account only")
			}
			if !readiness.StartupPricingReady {
				return fmt.Errorf("live mode requires a non-empty startup pricing watchlist")
			}
			if !readiness.EarningsUniverseReady {
				return fmt.Errorf("live mode requires a non-empty earnings watchlist")
			}
			if !readiness.RegimeDetectionEnabled {
				return fmt.Errorf("live mode requires regime detection to be enabled")
			}
			if !readiness.RegimeDetectorReady {
				return fmt.Errorf("live mode requires regime detection to have live market state access")
			}
		}
		if !readiness.RiskTokenConfigured {
			return fmt.Errorf("live mode requires an explicit RISK_TOKEN_SECRET with at least 32 characters")
		}
		return nil
	default:
		return fmt.Errorf("unknown runtime mode %q", readiness.Mode)
	}
}

func hasConfiguredRiskTokenSecret() bool {
	return len(strings.TrimSpace(os.Getenv("RISK_TOKEN_SECRET"))) >= 32
}
