package kalshi

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	mveMaxMarkupEnv = "KALSHI_MVE_MAX_MARKUP"
	mveMaxLegsEnv   = "KALSHI_MVE_MAX_LEGS"
)

type MVEFairValueConfig struct {
	MaxMarkup float64
	MaxLegs   int
}

type MVEFairValueReport struct {
	Ticker         string                      `json:"ticker"`
	Side           string                      `json:"side"`
	ComboPrice     float64                     `json:"combo_price"`
	FairPrice      float64                     `json:"fair_price"`
	Markup         float64                     `json:"markup"`
	MaxMarkup      float64                     `json:"max_markup"`
	Legs           []MVEFairValueLeg           `json:"legs"`
	Classification *MVEFairValueClassification `json:"classification,omitempty"`
	Allowed        bool                        `json:"allowed"`
	Reason         string                      `json:"reason,omitempty"`
}

type MVEFairValueLeg struct {
	EventTicker  string  `json:"event_ticker,omitempty"`
	MarketTicker string  `json:"market_ticker"`
	EventKey     string  `json:"event_key,omitempty"`
	Side         string  `json:"side"`
	AskPrice     float64 `json:"ask_price"`
}

type mveMarketClient interface {
	GetMarket(ctx context.Context, ticker string) (*MarketResponse, error)
}

func mveFairValueConfigFromEnv() MVEFairValueConfig {
	maxMarkup := readFloatEnv(mveMaxMarkupEnv, 0.03)
	if maxMarkup < 0 {
		maxMarkup = 0
	}
	maxLegs := readIntEnv(mveMaxLegsEnv, 3)
	if maxLegs < 0 {
		maxLegs = 0
	}
	return MVEFairValueConfig{MaxMarkup: maxMarkup, MaxLegs: maxLegs}
}

func (e *Executor) validateMVEFairValue(ctx context.Context, mapped MappedOrder) (*MVEFairValueReport, error) {
	ticker := strings.ToUpper(strings.TrimSpace(mapped.Request.Ticker))
	if !IsMultivariateTicker(ticker) {
		return nil, nil
	}
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: market client required for %s", ticker)
	}
	price, ok := mappedOrderLimitPrice(mapped.Request)
	if !ok {
		return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: order price required for %s", ticker)
	}
	report, err := EvaluateMVEFairValue(ctx, e.client, ticker, mapped.Request.Side, price, mveFairValueConfigFromEnv())
	if err != nil {
		return nil, err
	}
	if !report.Allowed {
		return report, fmt.Errorf("%s: combo=%.4f fair=%.4f markup=%.4f max=%.4f legs=%d",
			report.Reason,
			report.ComboPrice,
			report.FairPrice,
			report.Markup,
			report.MaxMarkup,
			len(report.Legs),
		)
	}
	return report, nil
}

func EvaluateMVEFairValue(ctx context.Context, client mveMarketClient, ticker, side string, comboPrice float64, cfg MVEFairValueConfig) (*MVEFairValueReport, error) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	side = strings.ToLower(strings.TrimSpace(side))
	report := &MVEFairValueReport{
		Ticker:     ticker,
		Side:       side,
		ComboPrice: comboPrice,
		MaxMarkup:  cfg.MaxMarkup,
	}
	if client == nil {
		return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: market client required")
	}
	if !IsMultivariateTicker(ticker) {
		report.Allowed = true
		return report, nil
	}
	if comboPrice <= 0 || comboPrice >= 1 || math.IsNaN(comboPrice) || math.IsInf(comboPrice, 0) {
		return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: invalid combo price %.4f", comboPrice)
	}
	if side != "yes" && side != "no" {
		return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: unsupported order side %q", side)
	}

	wrapperResp, err := client.GetMarket(ctx, ticker)
	if err != nil {
		return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: wrapper market %s: %w", ticker, err)
	}
	legs := wrapperResp.Market.MVESelectedLegs
	if len(legs) == 0 {
		return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: wrapper %s has no mve_selected_legs", ticker)
	}
	if cfg.MaxLegs > 0 && len(legs) > cfg.MaxLegs {
		report.Legs = summarizeMVELegs(legs)
		report.Classification = classifyMVEFairValueLegs(report.Legs)
		report.Reason = "kalshi_mve_leg_count_exceeded"
		return report, nil
	}

	selectedFair := 1.0
	for _, leg := range legs {
		legTicker := strings.ToUpper(strings.TrimSpace(leg.MarketTicker))
		if legTicker == "" {
			return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: wrapper %s contains leg without market_ticker", ticker)
		}
		legResp, err := client.GetMarket(ctx, legTicker)
		if err != nil {
			return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: leg market %s: %w", legTicker, err)
		}
		eventKey := canonicalMVEEventKey(leg, legResp.Market)
		legSide := strings.ToLower(strings.TrimSpace(leg.Side))
		ask, ok := legSideAskPrice(legResp.Market, legSide)
		if !ok {
			return nil, fmt.Errorf("kalshi_mve_fair_value_unavailable: leg market %s missing %s ask", legTicker, legSide)
		}
		selectedFair *= ask
		report.Legs = append(report.Legs, MVEFairValueLeg{
			EventTicker:  strings.ToUpper(strings.TrimSpace(leg.EventTicker)),
			MarketTicker: legTicker,
			EventKey:     eventKey,
			Side:         legSide,
			AskPrice:     ask,
		})
	}
	report.Classification = classifyMVEFairValueLegs(report.Legs)

	fair := selectedFair
	if side == "no" {
		fair = 1 - selectedFair
	}
	report.FairPrice = fair
	report.Markup = comboPrice - fair
	if fair <= 0 {
		report.Reason = "kalshi_mve_fair_value_zero"
		return report, nil
	}
	if comboPrice > fair*(1+cfg.MaxMarkup) {
		report.Reason = "kalshi_mve_fair_value_rejected"
		return report, nil
	}
	report.Allowed = true
	return report, nil
}

func summarizeMVELegs(legs []MVESelectedLeg) []MVEFairValueLeg {
	out := make([]MVEFairValueLeg, 0, len(legs))
	for _, leg := range legs {
		out = append(out, MVEFairValueLeg{
			EventTicker:  strings.ToUpper(strings.TrimSpace(leg.EventTicker)),
			MarketTicker: strings.ToUpper(strings.TrimSpace(leg.MarketTicker)),
			EventKey:     canonicalMVEEventKey(leg, Market{}),
			Side:         strings.ToLower(strings.TrimSpace(leg.Side)),
		})
	}
	return out
}

func legSideAskPrice(market Market, side string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "yes":
		if price, ok := parseMVEProbability(market.YesAskDollars); ok {
			return price, true
		}
		if noBid, ok := parseMVEProbability(market.NoBidDollars); ok {
			return 1 - noBid, true
		}
	case "no":
		if price, ok := parseMVEProbability(market.NoAskDollars); ok {
			return price, true
		}
		if yesBid, ok := parseMVEProbability(market.YesBidDollars); ok {
			return 1 - yesBid, true
		}
	}
	return 0, false
}

func mappedOrderLimitPrice(req OrderRequest) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(req.Side)) {
	case "yes":
		return parseMVEProbability(req.YesPriceDollars)
	case "no":
		return parseMVEProbability(req.NoPriceDollars)
	default:
		return 0, false
	}
}

func parseMVEProbability(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return 0, false
	}
	return value, true
}
