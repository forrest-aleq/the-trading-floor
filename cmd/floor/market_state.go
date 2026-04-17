package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/marketdata"
)

type marketStateProviderMode string

const (
	marketStateProviderNone       marketStateProviderMode = "none"
	marketStateProviderIBKRLegacy marketStateProviderMode = "ibkr_legacy"
	marketStateProviderPolygon    marketStateProviderMode = "polygon"
	marketStateProviderDatabento  marketStateProviderMode = "databento"
)

type configuredMarketState struct {
	Mode          marketStateProviderMode
	Provider      marketdata.SnapshotProvider
	RequestBudget marketdata.RequestBudget
	BrokerBacked  bool
}

func loadMarketStateProvider(snapshotClient marketdata.LegacyIBKRSnapshotClient, historicalClient marketdata.LegacyIBKRHistoricalClient, pacing *ibkr.PacingBudget) (configuredMarketState, error) {
	mode, err := readMarketStateProviderMode()
	if err != nil {
		return configuredMarketState{}, err
	}

	switch mode {
	case marketStateProviderNone:
		return configuredMarketState{Mode: mode}, nil
	case marketStateProviderIBKRLegacy:
		return configuredMarketState{
			Mode:          mode,
			Provider:      marketdata.NewLegacyIBKRProvider(snapshotClient, historicalClient),
			RequestBudget: ibkrRequestBudget{pacing: pacing},
			BrokerBacked:  true,
		}, nil
	case marketStateProviderPolygon:
		return configuredMarketState{}, fmt.Errorf("MARKET_STATE_PROVIDER=polygon is not implemented yet")
	case marketStateProviderDatabento:
		return configuredMarketState{}, fmt.Errorf("MARKET_STATE_PROVIDER=databento is not implemented yet")
	default:
		return configuredMarketState{}, fmt.Errorf("unsupported market state provider %q", mode)
	}
}

func readMarketStateProviderMode() (marketStateProviderMode, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("MARKET_STATE_PROVIDER")))
	if raw == "" {
		return marketStateProviderNone, nil
	}

	mode := marketStateProviderMode(raw)
	switch mode {
	case marketStateProviderNone, marketStateProviderIBKRLegacy, marketStateProviderPolygon, marketStateProviderDatabento:
		return mode, nil
	default:
		return "", fmt.Errorf("MARKET_STATE_PROVIDER must be one of none|ibkr_legacy|polygon|databento")
	}
}

type ibkrRequestBudget struct {
	pacing *ibkr.PacingBudget
}

func (b ibkrRequestBudget) Acquire(ctx context.Context) error {
	if b.pacing == nil {
		return nil
	}
	return b.pacing.AcquireMessage(ctx)
}
