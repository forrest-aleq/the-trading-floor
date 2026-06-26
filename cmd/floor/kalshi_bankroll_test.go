package main

import (
	"testing"
	"time"
)

func TestKalshiBankrollHeartbeatFieldsPreserveZeroCashAndRisk(t *testing.T) {
	snapshot := kalshiBankrollSnapshot{
		Available:             true,
		Source:                "kalshi_api",
		AccountEquity:         47.01,
		HasAccountEquity:      true,
		AvailableCash:         0,
		HasAvailableCash:      true,
		PortfolioValue:        47.01,
		HasPortfolioValue:     true,
		MaxOrderRisk:          10,
		EffectiveOrderRisk:    0,
		HasEffectiveOrderRisk: true,
		RiskPctEquity:         10,
		UpdatedAt:             time.Date(2026, 6, 26, 21, 30, 38, 0, time.UTC),
	}

	fields := anyFieldsToMap(t, snapshot.HeartbeatFields())

	if fields["kalshi_bankroll_available"] != true {
		t.Fatalf("expected Kalshi bankroll available field, got %#v", fields["kalshi_bankroll_available"])
	}
	if fields["kalshi_account_equity"] != 47.01 {
		t.Fatalf("expected account equity, got %#v", fields["kalshi_account_equity"])
	}
	if fields["kalshi_available_cash"] != float64(0) {
		t.Fatalf("expected zero available cash to be present, got %#v", fields["kalshi_available_cash"])
	}
	if fields["kalshi_effective_order_risk"] != float64(0) {
		t.Fatalf("expected zero effective risk to be present, got %#v", fields["kalshi_effective_order_risk"])
	}
	if fields["kalshi_portfolio_value"] != 47.01 {
		t.Fatalf("expected portfolio value, got %#v", fields["kalshi_portfolio_value"])
	}
}

func TestKalshiBankrollHeartbeatFieldsDoNotInventUnknownCash(t *testing.T) {
	snapshot := kalshiBankrollSnapshot{
		Available:        true,
		Source:           "configured_account_equity",
		AccountEquity:    100,
		HasAccountEquity: true,
		MaxOrderRisk:     5,
		RiskPctEquity:    2,
		UpdatedAt:        time.Date(2026, 6, 26, 21, 30, 38, 0, time.UTC),
	}

	fields := anyFieldsToMap(t, snapshot.HeartbeatFields())

	if _, ok := fields["kalshi_available_cash"]; ok {
		t.Fatalf("did not expect unavailable cash to be invented, got %#v", fields["kalshi_available_cash"])
	}
	if _, ok := fields["kalshi_effective_order_risk"]; ok {
		t.Fatalf("did not expect unavailable effective risk to be invented, got %#v", fields["kalshi_effective_order_risk"])
	}
}

func TestReadKalshiBankrollSnapshotAllowsConfiguredZeroEquity(t *testing.T) {
	t.Setenv("KALSHI_ACCOUNT_EQUITY_DOLLARS", "0")
	t.Setenv("KALSHI_MAX_ORDER_DOLLARS", "10")
	t.Setenv("KALSHI_RISK_PCT_EQUITY", "5")

	snapshot := readKalshiBankrollSnapshot(t.Context(), nil)

	if !snapshot.Available {
		t.Fatal("expected explicitly configured zero equity to be available")
	}
	if snapshot.Source != "configured_account_equity" {
		t.Fatalf("expected configured_account_equity source, got %q", snapshot.Source)
	}
	if !snapshot.HasAccountEquity || snapshot.AccountEquity != 0 {
		t.Fatalf("expected configured zero account equity, got has=%v equity=%.2f", snapshot.HasAccountEquity, snapshot.AccountEquity)
	}
}

func TestReadKalshiBankrollSnapshotRejectsInvalidConfiguredEquity(t *testing.T) {
	t.Setenv("KALSHI_ACCOUNT_EQUITY_DOLLARS", "-1")

	snapshot := readKalshiBankrollSnapshot(t.Context(), nil)

	if snapshot.Available {
		t.Fatal("expected invalid configured equity to be unavailable")
	}
	if snapshot.Reason != "invalid_configured_account_equity" {
		t.Fatalf("expected invalid config reason, got %q", snapshot.Reason)
	}
}

func anyFieldsToMap(t testing.TB, fields []any) map[string]any {
	t.Helper()
	if len(fields)%2 != 0 {
		t.Fatalf("expected alternating key/value fields, got %d entries", len(fields))
	}
	out := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			t.Fatalf("expected string key at index %d, got %T", i, fields[i])
		}
		out[key] = fields[i+1]
	}
	return out
}
