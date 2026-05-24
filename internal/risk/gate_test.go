package risk

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
)

func TestLoadTokenSecretRejectsShortConfiguredSecret(t *testing.T) {
	t.Setenv("RISK_TOKEN_SECRET", "too-short")

	defer func() {
		if recover() == nil {
			t.Fatal("expected short configured secret to panic")
		}
	}()

	_ = loadTokenSecret(slog.Default())
}

func TestLoadTokenSecretGeneratesEphemeralSecretWhenUnset(t *testing.T) {
	t.Setenv("RISK_TOKEN_SECRET", "")

	secret := loadTokenSecret(slog.Default())
	if len(secret) < 32 {
		t.Fatalf("expected generated secret to be at least 32 bytes, got %d", len(secret))
	}
}

func TestCapabilityTokenValidatesExactOrder(t *testing.T) {
	t.Setenv("RISK_TOKEN_SECRET", strings.Repeat("x", 32))

	gate := NewGate(DefaultLimits())
	order := model.Order{
		ID:         "order-token",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   5,
		LimitPrice: 100,
		Notional:   500,
	}
	thesis := &model.Thesis{Conviction: 0.9}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if !decision.Allowed || decision.Token == nil || decision.AdjustedOrder == nil {
		t.Fatalf("expected allowed decision with token, got %+v", decision)
	}
	if err := gate.ValidateCapabilityToken(decision.Token, *decision.AdjustedOrder); err != nil {
		t.Fatalf("expected token to validate: %v", err)
	}

	tampered := *decision.AdjustedOrder
	tampered.Quantity++
	if err := gate.ValidateCapabilityToken(decision.Token, tampered); err == nil {
		t.Fatal("expected modified order to fail token validation")
	}

	priceTampered := *decision.AdjustedOrder
	priceTampered.LimitPrice++
	if err := gate.ValidateCapabilityToken(decision.Token, priceTampered); err == nil {
		t.Fatal("expected modified limit price to fail token validation")
	}

	expired := *decision.Token
	expired.Expiry = time.Now().UTC().Add(-time.Second)
	if err := gate.ValidateCapabilityToken(&expired, *decision.AdjustedOrder); err == nil {
		t.Fatal("expected expired token to fail validation")
	}
}

func TestParseLimitsLoadsEmbeddedShape(t *testing.T) {
	limits, err := parseLimits([]byte(`{
		"max_daily_loss_pct": 2.5,
		"max_single_position_pct": 12,
		"max_correlated_positions": 4,
		"max_open_positions": 8,
			"capital_per_desk": 30000,
			"max_position_size_pct": 8,
			"min_conviction_score": 0.72,
			"max_quote_age_seconds": 60,
			"max_equity_spread_bps": 25,
			"max_option_spread_bps": 250,
			"total_capital": 2000000,
			"max_factor_exposure_pct": 18,
			"max_drawdown_pct": 9,
		"kill_switch_drawdown_pct": 14,
		"max_gross_exposure_pct": 160,
		"max_net_exposure_pct": 90,
		"max_cash_deploy_pct": 70
	}`))
	if err != nil {
		t.Fatalf("parse limits: %v", err)
	}
	if limits.MinConvictionScore != 0.72 || limits.CapitalPerDesk != 30000 || limits.MaxQuoteAgeSeconds != 60 {
		t.Fatalf("unexpected limits parse result: %+v", limits)
	}
}

func TestParseLimitsRejectsInvalidValues(t *testing.T) {
	_, err := parseLimits([]byte(`{
		"max_daily_loss_pct": 0,
		"max_single_position_pct": 12,
		"max_open_positions": 8,
			"capital_per_desk": 30000,
			"min_conviction_score": 1.2,
			"max_quote_age_seconds": -1,
			"total_capital": 2000000,
			"max_gross_exposure_pct": 160,
			"max_cash_deploy_pct": 70
	}`))
	if err == nil {
		t.Fatal("expected invalid limits to fail")
	}
	if !strings.Contains(err.Error(), "max_daily_loss_pct") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestGateRejectsStaleQuoteAge(t *testing.T) {
	gate := NewGate(DefaultLimits())

	order := model.Order{
		ID:         "order-stale-quote",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "SPY", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   10,
		LimitPrice: 500,
		Notional:   5000,
	}
	thesis := &model.Thesis{
		Conviction: 0.9,
		MarketContext: &model.MarketContext{
			QuoteAgeSeconds: DefaultLimits().MaxQuoteAgeSeconds + 30,
			SpreadBps:       5,
		},
	}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected stale quote to be rejected")
	}
	found := false
	for _, violation := range decision.Violations {
		if violation.Rule == "stale_quote" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected stale_quote violation, got %+v", decision.Violations)
	}
}

func TestGateRejectsMaxPositionSizePct(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxPositionSizePct = 5
	limits.MaxSinglePositionPct = 20
	gate := NewGate(limits)

	order := model.Order{
		ID:         "order-position-size",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   20,
		LimitPrice: 100,
		Notional:   2000,
	}
	thesis := &model.Thesis{Conviction: 0.9}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected oversized per-trade position to be rejected")
	}
	found := false
	for _, violation := range decision.Violations {
		if violation.Rule == "max_position_size_pct" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected max_position_size_pct violation, got %+v", decision.Violations)
	}
}

func TestGateRejectsWideEquitySpread(t *testing.T) {
	gate := NewGate(DefaultLimits())

	order := model.Order{
		ID:         "order-wide-spread",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "QQQ", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   10,
		LimitPrice: 450,
		Notional:   4500,
	}
	thesis := &model.Thesis{
		Conviction: 0.9,
		MarketContext: &model.MarketContext{
			QuoteAgeSeconds: 3,
			SpreadBps:       DefaultLimits().MaxEquitySpreadBps + 12,
		},
	}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected wide spread to be rejected")
	}
	found := false
	for _, violation := range decision.Violations {
		if violation.Rule == "quote_spread_too_wide" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected quote_spread_too_wide violation, got %+v", decision.Violations)
	}
}

func TestGateAllowsDefinedRiskBullCallSpreadUsingMaxLoss(t *testing.T) {
	gate := NewGate(DefaultLimits())

	lower := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   "20260619",
		Strike:   120,
		Right:    "C",
	}
	higher := lower
	higher.Strike = 130

	order := model.Order{
		ID:         "spread-1",
		DeskID:     "desk-a",
		Structure:  "bull_call_spread",
		Direction:  model.Long,
		Quantity:   1,
		LimitPrice: 3.50,
		Notional:   15000,
		Legs: []model.TradeLeg{
			{Instrument: lower, Direction: model.Long, Ratio: 1},
			{Instrument: higher, Direction: model.Short, Ratio: 1},
		},
	}
	thesis := &model.Thesis{Conviction: 0.9}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if !decision.Allowed {
		t.Fatalf("expected defined-risk bull call spread to be allowed, got %+v", decision.Violations)
	}
}

func TestGateRejectsUnsupportedMultiLegStructure(t *testing.T) {
	gate := NewGate(DefaultLimits())

	callJun := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   "20260619",
		Strike:   120,
		Right:    "C",
	}
	callSep := callJun
	callSep.Expiry = "20260918"

	order := model.Order{
		ID:         "spread-2",
		DeskID:     "desk-a",
		Structure:  "calendar_spread",
		Direction:  model.Long,
		Quantity:   1,
		LimitPrice: 4.0,
		Notional:   400,
		Legs: []model.TradeLeg{
			{Instrument: callJun, Direction: model.Long, Ratio: 1},
			{Instrument: callSep, Direction: model.Short, Ratio: 1},
		},
	}
	thesis := &model.Thesis{Conviction: 0.9}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected unsupported multi-leg structure to be rejected")
	}
	if len(decision.Violations) == 0 || decision.Violations[0].Rule != "unsupported_multi_leg_structure" {
		t.Fatalf("expected unsupported multi-leg violation, got %+v", decision.Violations)
	}
}

func TestGateRejectsStaleEvidence(t *testing.T) {
	gate := NewGate(DefaultLimits())

	order := model.Order{
		ID:         "order-1",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   10,
		LimitPrice: 100,
		Notional:   1000,
	}
	thesis := &model.Thesis{
		Conviction: 0.9,
		EvidenceMeta: &evidence.Metadata{
			SourceTrust:        0.86,
			FreshnessStatus:    "stale",
			FreshnessReason:    "stale_news",
			EvidenceScore:      0.22,
			ContradictionCount: 0,
		},
	}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected stale evidence to be rejected")
	}

	found := false
	for _, violation := range decision.Violations {
		if violation.Rule == "stale_signal_evidence" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected stale_signal_evidence violation, got %+v", decision.Violations)
	}
}

func TestGateUsesQuantMarginEstimateForSingleNameRisk(t *testing.T) {
	gate := NewGate(DefaultLimits())

	order := model.Order{
		ID:         "order-short-1",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Short,
		Quantity:   10,
		LimitPrice: 100,
		Notional:   1000,
	}
	thesis := &model.Thesis{
		Conviction: 0.9,
		QuantMetrics: &model.QuantMetrics{
			MarginEstimate: 30000,
		},
	}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected oversized quant margin estimate to be rejected")
	}
	found := false
	for _, violation := range decision.Violations {
		if violation.Rule == "max_single_position_pct" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected max_single_position_pct violation, got %+v", decision.Violations)
	}
}

func TestGateRejectsLowMarketMappingConfidence(t *testing.T) {
	gate := NewGate(DefaultLimits())

	order := model.Order{
		ID:         "order-2",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   10,
		LimitPrice: 100,
		Notional:   1000,
	}
	thesis := &model.Thesis{
		Conviction: 0.9,
		EvidenceMeta: &evidence.Metadata{
			SourceTrust:     0.92,
			FreshnessStatus: "fresh",
			EvidenceScore:   0.44,
			ConfidenceVector: &evidence.ConfidenceVector{
				FactConfidence:          0.88,
				NoveltyConfidence:       0.55,
				MarketMappingConfidence: 0.18,
				ExpressionConfidence:    0.61,
				ExecutionConfidence:     0.63,
				CompetenceConfidence:    0.57,
			},
		},
	}
	portfolio := PortfolioState{
		NAV:           100000,
		Cash:          100000,
		DeskPositions: map[string]int{},
		DeskDailyPnL:  map[string]float64{},
		DeskCapital:   map[string]float64{"desk-a": 25000},
	}

	decision := gate.Check(order, thesis, portfolio)
	if decision.Allowed {
		t.Fatal("expected low market-mapping confidence to be rejected")
	}

	found := false
	for _, violation := range decision.Violations {
		if violation.Rule == "low_market_mapping_confidence" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected low_market_mapping_confidence violation, got %+v", decision.Violations)
	}
}
