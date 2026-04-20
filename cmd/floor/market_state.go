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
	marketStateProviderFMP        marketStateProviderMode = "fmp"
	marketStateProviderPolygon    marketStateProviderMode = "polygon"
	marketStateProviderDatabento  marketStateProviderMode = "databento"
	marketStateProviderMassive    marketStateProviderMode = "massive"
)

type configuredMarketState struct {
	Mode          marketStateProviderMode
	Label         string
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
			Label:         string(mode),
			Provider:      marketdata.NewLegacyIBKRProvider(snapshotClient, historicalClient),
			RequestBudget: ibkrRequestBudget{pacing: pacing},
			BrokerBacked:  true,
		}, nil
	case marketStateProviderFMP:
		provider, err := marketdata.NewFMPProvider("")
		if err != nil {
			return configuredMarketState{}, err
		}
		return configuredMarketState{
			Mode:     mode,
			Label:    string(mode),
			Provider: provider,
		}, nil
	case marketStateProviderPolygon:
		label, provider, err := loadMassiveBackedProvider()
		if err != nil {
			return configuredMarketState{}, err
		}
		return configuredMarketState{
			Mode:     mode,
			Label:    label,
			Provider: provider,
		}, nil
	case marketStateProviderDatabento:
		return configuredMarketState{}, fmt.Errorf("MARKET_DATA_PROVIDER=databento is not implemented yet")
	default:
		return configuredMarketState{}, fmt.Errorf("unsupported market data provider %q", mode)
	}
}

func loadMassiveBackedProvider() (string, marketdata.SnapshotProvider, error) {
	polygonProvider, err := marketdata.NewPolygonProvider("")
	if err != nil {
		return "", nil, err
	}

	switch marketdata.ResolveMassivePlan() {
	case marketdata.MassivePlanBasicFree:
		snapshotProvider := marketdata.NewHistoricalSnapshotProvider(polygonProvider)
		return "massive_free+polygon_agg_snapshots", marketdata.NewSplitProvider(snapshotProvider, polygonProvider), nil
	default:
		var provider marketdata.SnapshotProvider = polygonProvider
		label := "massive"
		if fallback, err := marketdata.NewFMPProvider(""); err == nil {
			provider = marketdata.NewFallbackProvider(provider, fallback)
			label = "massive+fmp_fallback"
		}
		return label, provider, nil
	}
}

func readMarketStateProviderMode() (marketStateProviderMode, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("MARKET_DATA_PROVIDER")))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(os.Getenv("MARKET_STATE_PROVIDER")))
	}
	if raw == "" && marketdata.ResolveDefaultMarketDataProvider() != "" {
		raw = marketdata.ResolveDefaultMarketDataProvider()
	}
	if raw == "" {
		return marketStateProviderNone, nil
	}

	mode := marketStateProviderMode(raw)
	if mode == marketStateProviderMassive {
		mode = marketStateProviderPolygon
	}
	switch mode {
	case marketStateProviderNone, marketStateProviderIBKRLegacy, marketStateProviderFMP, marketStateProviderPolygon, marketStateProviderDatabento:
		return mode, nil
	default:
		return "", fmt.Errorf("MARKET_DATA_PROVIDER must be one of none|ibkr_legacy|fmp|polygon|massive|databento")
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
