package kalshi

import (
	"context"
	"fmt"
	"testing"
)

func TestMultivariateTickerDetection(t *testing.T) {
	t.Setenv(unsafeAllowMVEWrappersEnv, "false")

	if !IsMultivariateTicker("KXMVESPORTSMULTIGAMEEXTENDED-S202601A7277A770-22D4C50549A") {
		t.Fatal("expected KXMVE wrapper ticker to be detected")
	}
	if IsMultivariateTicker("KXWCGAME-26JUN26NORFRA-FRA") {
		t.Fatal("expected normal Kalshi market ticker to remain non-MVE")
	}
	if !ShouldBlockMultivariateTicker("kxmvEcRosScategory-s2026-test") {
		t.Fatal("expected MVE wrapper to be blocked by default")
	}
}

func TestEvaluateMVEFairValueClassifiesSameMatchAcrossDistinctEventTickers(t *testing.T) {
	client := staticMVEMarketClient{
		"KXMVESPORTS-SAME": {Market: Market{
			Ticker: "KXMVESPORTS-SAME",
			MVESelectedLegs: []MVESelectedLeg{
				{EventTicker: "KXWCGAME-26JUN26NORFRA", MarketTicker: "KXWCGAME-26JUN26NORFRA-FRA", Side: "yes"},
				{EventTicker: "KXWCFIRSTHALF-26JUN26NORFRA", MarketTicker: "KXWCFIRSTHALF-26JUN26NORFRA-FRA", Side: "yes"},
				{EventTicker: "KXWCBTTS-26JUN26NORFRA", MarketTicker: "KXWCBTTS-26JUN26NORFRA-YES", Side: "yes"},
			},
		}},
		"KXWCGAME-26JUN26NORFRA-FRA": {Market: Market{
			Ticker:        "KXWCGAME-26JUN26NORFRA-FRA",
			Title:         "Norway vs France: Winner",
			YesAskDollars: "0.6200",
			EventTicker:   "KXWCGAME-26JUN26NORFRA",
		}},
		"KXWCFIRSTHALF-26JUN26NORFRA-FRA": {Market: Market{
			Ticker:        "KXWCFIRSTHALF-26JUN26NORFRA-FRA",
			Title:         "Norway vs France: First Half Winner",
			YesAskDollars: "0.5500",
			EventTicker:   "KXWCFIRSTHALF-26JUN26NORFRA",
		}},
		"KXWCBTTS-26JUN26NORFRA-YES": {Market: Market{
			Ticker:        "KXWCBTTS-26JUN26NORFRA-YES",
			Title:         "Norway vs France: Both Teams To Score",
			YesAskDollars: "0.4900",
			EventTicker:   "KXWCBTTS-26JUN26NORFRA",
		}},
	}

	report, err := EvaluateMVEFairValue(context.Background(), client, "KXMVESPORTS-SAME", "yes", 0.16, MVEFairValueConfig{MaxMarkup: 0.03, MaxLegs: 5})
	if err != nil {
		t.Fatal(err)
	}
	if report.Classification == nil {
		t.Fatal("expected classification")
	}
	if report.Classification.Class != MVEClassSameMatch {
		t.Fatalf("class = %q, want %q (%+v)", report.Classification.Class, MVEClassSameMatch, report.Classification)
	}
	if report.Classification.UniqueEventKeys != 1 {
		t.Fatalf("unique event keys = %d, want 1 (%+v)", report.Classification.UniqueEventKeys, report.Classification)
	}
}

type staticMVEMarketClient map[string]MarketResponse

func (s staticMVEMarketClient) GetMarket(_ context.Context, ticker string) (*MarketResponse, error) {
	resp, ok := s[ticker]
	if !ok {
		return nil, fmt.Errorf("missing market %s", ticker)
	}
	return &resp, nil
}
