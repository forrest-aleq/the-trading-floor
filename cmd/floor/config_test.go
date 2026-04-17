package main

import (
	"testing"

	"github.com/hnic/trading-floor/internal/wire"
)

func TestFullDeskConfigBalancedAB(t *testing.T) {
	desks := fullDeskConfig()
	if len(desks) != 40 {
		t.Fatalf("expected 40 desks, got %d", len(desks))
	}

	groupCounts := map[string]int{}
	domainCounts := map[string]int{}
	for _, desk := range desks {
		groupCounts[desk.group]++
		domainCounts[desk.domain]++
	}

	if groupCounts["A"] != 20 || groupCounts["B"] != 20 {
		t.Fatalf("expected balanced 20/20 split, got A=%d B=%d", groupCounts["A"], groupCounts["B"])
	}

	if len(domainCounts) != 8 {
		t.Fatalf("expected 8 domains, got %d", len(domainCounts))
	}

	for domain, count := range domainCounts {
		if count != 5 {
			t.Fatalf("expected 5 desks for domain %s, got %d", domain, count)
		}
	}
}

func TestRegisterDefaultFeedsRegistersExtendedWireSurface(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	t.Setenv("FMP_API_KEY", "")
	t.Setenv("EARNINGS_API_KEY", "")
	t.Setenv("TELEGRAM_FEED_URLS", "")
	t.Setenv("ALT_DATA_SOURCES", "")

	wireMgr := wire.NewManager()
	count := registerDefaultFeeds(wireMgr, &fakeBroker{})
	if count != 8 {
		t.Fatalf("expected 8 registered feeds, got %d", count)
	}

	stats := wireMgr.Stats()
	if stats.ActiveFeeds != 8 {
		t.Fatalf("expected wire manager to track 8 active feeds, got %d", stats.ActiveFeeds)
	}
}

func TestLoadRuntimeModeDefaultsToDev(t *testing.T) {
	t.Setenv("FLOOR_RUNTIME_MODE", "")

	mode, err := loadRuntimeMode()
	if err != nil {
		t.Fatalf("loadRuntimeMode returned error: %v", err)
	}
	if mode != runtimeModeDev {
		t.Fatalf("runtime mode = %s, want %s", mode, runtimeModeDev)
	}
}

func TestLoadRuntimeModeRejectsUnknownValue(t *testing.T) {
	t.Setenv("FLOOR_RUNTIME_MODE", "chaos")

	if _, err := loadRuntimeMode(); err == nil {
		t.Fatal("expected invalid runtime mode to return error")
	}
}

func TestValidateRuntimeReadinessRequiresPaperBrokerForPaperMode(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                   runtimeModePaper,
		DBReady:                true,
		BrokerConnected:        true,
		BrokerPaper:            false,
		StartupPricingReady:    true,
		RegimeDetectionEnabled: true,
	})
	if err == nil {
		t.Fatal("expected paper readiness validation to fail without paper broker")
	}
}

func TestValidateRuntimeReadinessRequiresExplicitRiskTokenForLiveMode(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                   runtimeModeLive,
		DBReady:                true,
		BrokerConnected:        true,
		BrokerPaper:            false,
		StartupPricingReady:    true,
		EarningsUniverseReady:  true,
		RegimeDetectionEnabled: true,
		RiskTokenConfigured:    false,
	})
	if err == nil {
		t.Fatal("expected live readiness validation to fail without explicit risk token")
	}
}
