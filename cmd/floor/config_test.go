package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/marketdata"
	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/pkg/model"
)

type stubMarketStateProvider struct{}

func (stubMarketStateProvider) Snapshot(context.Context, model.Instrument) (*marketdata.Snapshot, error) {
	return &marketdata.Snapshot{
		Last:       100,
		Bid:        99.5,
		Ask:        100.5,
		Volume:     1000,
		ObservedAt: time.Now().UTC(),
	}, nil
}

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

func TestActiveDeskConfigUsesAllowlist(t *testing.T) {
	t.Setenv("FLOOR_ENABLED_DESKS", "corp-earnings-a,macro-rates-a,sector-tech-a")
	t.Setenv("FLOOR_DESK_LIMIT", "")
	t.Setenv("FLOOR_ENABLE_KALSHI_DESKS", "")
	t.Setenv("FLOOR_ENABLE_PREDICTION_MARKET_DESK", "")

	desks, err := activeDeskConfig()
	if err != nil {
		t.Fatalf("activeDeskConfig returned error: %v", err)
	}
	if len(desks) != 3 {
		t.Fatalf("expected 3 desks, got %d", len(desks))
	}
	if desks[0].id != "corp-earnings-a" || desks[1].id != "macro-rates-a" || desks[2].id != "sector-tech-a" {
		t.Fatalf("unexpected desk selection: %#v", desks)
	}
}

func TestActiveDeskConfigCanEnablePredictionMarketDesk(t *testing.T) {
	t.Setenv("FLOOR_ENABLED_DESKS", "pred-markets-a")
	t.Setenv("FLOOR_DESK_LIMIT", "")
	t.Setenv("FLOOR_ENABLE_KALSHI_DESKS", "")
	t.Setenv("FLOOR_ENABLE_PREDICTION_MARKET_DESK", "true")

	desks, err := activeDeskConfig()
	if err != nil {
		t.Fatalf("activeDeskConfig returned error: %v", err)
	}
	if len(desks) != 1 {
		t.Fatalf("expected 1 desk, got %d", len(desks))
	}
	if desks[0].id != "pred-markets-a" || desks[0].domain != "prediction_market" {
		t.Fatalf("unexpected prediction-market desk: %#v", desks[0])
	}
}

func TestActiveDeskConfigCanEnableKalshiDeskPack(t *testing.T) {
	t.Setenv("FLOOR_ENABLED_DESKS", "kalshi-rates-a,kalshi-weather-a")
	t.Setenv("FLOOR_DESK_LIMIT", "")
	t.Setenv("FLOOR_ENABLE_KALSHI_DESKS", "true")
	t.Setenv("FLOOR_ENABLE_PREDICTION_MARKET_DESK", "")
	t.Setenv("KALSHI_DESK_CAPITAL_DOLLARS", "")

	desks, err := activeDeskConfig()
	if err != nil {
		t.Fatalf("activeDeskConfig returned error: %v", err)
	}
	if len(desks) != 2 {
		t.Fatalf("expected 2 desks, got %d", len(desks))
	}
	for _, desk := range desks {
		if desk.domain != "prediction_market" {
			t.Fatalf("expected prediction_market domain, got %#v", desk)
		}
		if desk.capital != 0 {
			t.Fatalf("expected default Kalshi desk capital to be unset, got %.2f", desk.capital)
		}
	}
}

func TestKalshiDeskConfigUsesOnlyExplicitDeskCapital(t *testing.T) {
	t.Setenv("KALSHI_DESK_CAPITAL_DOLLARS", "7.50")

	desks := kalshiDeskConfig()
	if len(desks) == 0 {
		t.Fatal("expected Kalshi desk config")
	}
	for _, desk := range desks {
		if desk.capital != 7.50 {
			t.Fatalf("expected explicit Kalshi desk capital 7.50, got %.2f", desk.capital)
		}
	}
}

func TestLoadDecisionThresholdsUsesEnv(t *testing.T) {
	t.Setenv("SCANNER_MIN_SCORE", "52")
	t.Setenv("RESEARCH_MIN_CONVICTION", "0.54")
	t.Setenv("DESK_MIN_CONVICTION", "0.56")
	t.Setenv("DESK_COUNCIL_THRESHOLD", "0.04")

	cfg := loadDecisionThresholds()
	if cfg.ScannerMinScore != 52 {
		t.Fatalf("scanner min score = %.2f, want 52", cfg.ScannerMinScore)
	}
	if cfg.ResearchMinConviction != 0.54 {
		t.Fatalf("research min conviction = %.2f, want 0.54", cfg.ResearchMinConviction)
	}
	if cfg.DeskMinConviction != 0.56 {
		t.Fatalf("desk min conviction = %.2f, want 0.56", cfg.DeskMinConviction)
	}
	if cfg.CouncilThreshold != 0.04 {
		t.Fatalf("council threshold = %.2f, want 0.04", cfg.CouncilThreshold)
	}
}

func TestLoadDecisionThresholdsFallsBackOnInvalidEnv(t *testing.T) {
	t.Setenv("SCANNER_MIN_SCORE", "150")
	t.Setenv("RESEARCH_MIN_CONVICTION", "-1")
	t.Setenv("DESK_MIN_CONVICTION", "2")
	t.Setenv("DESK_COUNCIL_THRESHOLD", "not-a-number")

	cfg := loadDecisionThresholds()
	if cfg.ScannerMinScore != 70 {
		t.Fatalf("scanner min score = %.2f, want default 70", cfg.ScannerMinScore)
	}
	if cfg.ResearchMinConviction != 0.65 {
		t.Fatalf("research min conviction = %.2f, want default 0.65", cfg.ResearchMinConviction)
	}
	if cfg.DeskMinConviction != cfg.ResearchMinConviction {
		t.Fatalf("desk min conviction = %.2f, want research default %.2f", cfg.DeskMinConviction, cfg.ResearchMinConviction)
	}
	if cfg.CouncilThreshold != 0.02 {
		t.Fatalf("council threshold = %.2f, want default 0.02", cfg.CouncilThreshold)
	}
}

func TestActiveDeskConfigRejectsUnknownAllowlistDesk(t *testing.T) {
	t.Setenv("FLOOR_ENABLED_DESKS", "corp-earnings-a,no-such-desk")
	t.Setenv("FLOOR_DESK_LIMIT", "")
	t.Setenv("FLOOR_ENABLE_KALSHI_DESKS", "")
	t.Setenv("FLOOR_ENABLE_PREDICTION_MARKET_DESK", "")

	if _, err := activeDeskConfig(); err == nil {
		t.Fatal("expected unknown desk allowlist entry to fail")
	}
}

func TestActiveDeskConfigAppliesDeskLimit(t *testing.T) {
	t.Setenv("FLOOR_ENABLED_DESKS", "")
	t.Setenv("FLOOR_DESK_LIMIT", "5")
	t.Setenv("FLOOR_ENABLE_KALSHI_DESKS", "")
	t.Setenv("FLOOR_ENABLE_PREDICTION_MARKET_DESK", "")

	desks, err := activeDeskConfig()
	if err != nil {
		t.Fatalf("activeDeskConfig returned error: %v", err)
	}
	if len(desks) != 5 {
		t.Fatalf("expected 5 desks, got %d", len(desks))
	}
}

func TestRegisterDefaultFeedsRegistersExtendedWireSurface(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	t.Setenv("FMP_API_KEY", "")
	t.Setenv("EARNINGS_API_KEY", "")
	t.Setenv("TELEGRAM_FEED_URLS", "")
	t.Setenv("ALT_DATA_SOURCES", "")
	t.Setenv("KALSHI_FEED_ENABLED", "")

	wireMgr := wire.NewManager()
	count := registerDefaultFeeds(wireMgr, stubMarketStateProvider{}, fullDeskConfig())
	if count != 8 {
		t.Fatalf("expected 8 registered feeds, got %d", count)
	}

	stats := wireMgr.Stats()
	if stats.ActiveFeeds != 8 {
		t.Fatalf("expected wire manager to track 8 active feeds, got %d", stats.ActiveFeeds)
	}
}

func TestRegisterDefaultFeedsNarrowsToKalshiForKalshiOnlyDesks(t *testing.T) {
	t.Setenv("KALSHI_FEED_ENABLED", "true")

	wireMgr := wire.NewManager()
	count := registerDefaultFeeds(wireMgr, stubMarketStateProvider{}, kalshiDeskConfig())
	if count != 1 {
		t.Fatalf("expected only Kalshi feed for Kalshi-only runtime, got %d", count)
	}

	stats := wireMgr.Stats()
	if stats.ActiveFeeds != 1 {
		t.Fatalf("expected wire manager to track 1 active feed, got %d", stats.ActiveFeeds)
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

func TestReadMarketStateProviderDefaultsToNone(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "")
	t.Setenv("MARKET_STATE_PROVIDER", "")
	t.Setenv("FMP_API_KEY", "")
	t.Setenv("EARNINGS_API_KEY", "")

	mode, err := readMarketStateProviderMode()
	if err != nil {
		t.Fatalf("readMarketStateProviderMode returned error: %v", err)
	}
	if mode != marketStateProviderNone {
		t.Fatalf("market data provider = %s, want %s", mode, marketStateProviderNone)
	}
}

func TestReadMarketStateProviderUsesNewEnvName(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "fmp")
	t.Setenv("MARKET_STATE_PROVIDER", "")

	mode, err := readMarketStateProviderMode()
	if err != nil {
		t.Fatalf("readMarketStateProviderMode returned error: %v", err)
	}
	if mode != marketStateProviderFMP {
		t.Fatalf("market data provider = %s, want %s", mode, marketStateProviderFMP)
	}
}

func TestReadMarketStateProviderDefaultsToFMPWhenKeyExists(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "")
	t.Setenv("MARKET_STATE_PROVIDER", "")
	t.Setenv("POLYGON_API_KEY", "")
	t.Setenv("MASSIVE_API_KEY", "")
	t.Setenv("FMP_API_KEY", "test-key")

	mode, err := readMarketStateProviderMode()
	if err != nil {
		t.Fatalf("readMarketStateProviderMode returned error: %v", err)
	}
	if mode != marketStateProviderFMP {
		t.Fatalf("market data provider = %s, want %s", mode, marketStateProviderFMP)
	}
}

func TestReadMarketStateProviderUsesPolygonFromNewEnvName(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "polygon")
	t.Setenv("MARKET_STATE_PROVIDER", "")

	mode, err := readMarketStateProviderMode()
	if err != nil {
		t.Fatalf("readMarketStateProviderMode returned error: %v", err)
	}
	if mode != marketStateProviderPolygon {
		t.Fatalf("market data provider = %s, want %s", mode, marketStateProviderPolygon)
	}
}

func TestReadMarketStateProviderSupportsMassiveAlias(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "massive")
	t.Setenv("MARKET_STATE_PROVIDER", "")

	mode, err := readMarketStateProviderMode()
	if err != nil {
		t.Fatalf("readMarketStateProviderMode returned error: %v", err)
	}
	if mode != marketStateProviderPolygon {
		t.Fatalf("market data provider = %s, want %s", mode, marketStateProviderPolygon)
	}
}

func TestReadMarketStateProviderDefaultsToPolygonWhenKeyExists(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "")
	t.Setenv("MARKET_STATE_PROVIDER", "")
	t.Setenv("POLYGON_API_KEY", "test-key")
	t.Setenv("MASSIVE_API_KEY", "")
	t.Setenv("FMP_API_KEY", "fallback-key")

	mode, err := readMarketStateProviderMode()
	if err != nil {
		t.Fatalf("readMarketStateProviderMode returned error: %v", err)
	}
	if mode != marketStateProviderPolygon {
		t.Fatalf("market data provider = %s, want %s", mode, marketStateProviderPolygon)
	}
}

func TestReadMarketStateProviderDefaultsToPolygonWhenMassiveKeyExists(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "")
	t.Setenv("MARKET_STATE_PROVIDER", "")
	t.Setenv("POLYGON_API_KEY", "")
	t.Setenv("MASSIVE_API_KEY", "test-key")
	t.Setenv("FMP_API_KEY", "")

	mode, err := readMarketStateProviderMode()
	if err != nil {
		t.Fatalf("readMarketStateProviderMode returned error: %v", err)
	}
	if mode != marketStateProviderPolygon {
		t.Fatalf("market data provider = %s, want %s", mode, marketStateProviderPolygon)
	}
}

func TestReadMarketStateProviderDefaultsToMassiveAlias(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "")
	t.Setenv("MARKET_STATE_PROVIDER", "")
	t.Setenv("POLYGON_API_KEY", "test-key")
	t.Setenv("MASSIVE_API_KEY", "")
	t.Setenv("FMP_API_KEY", "")

	mode, err := readMarketStateProviderMode()
	if err != nil {
		t.Fatalf("readMarketStateProviderMode returned error: %v", err)
	}
	if mode != marketStateProviderPolygon {
		t.Fatalf("market data provider = %s, want %s", mode, marketStateProviderPolygon)
	}
}

func TestValidateRuntimeReadinessRequiresPaperBrokerForPaperMode(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                    runtimeModePaper,
		DBReady:                 true,
		BrokerExecutionRequired: true,
		BrokerConnected:         true,
		BrokerPaper:             false,
		MarketStateConfigured:   true,
		StartupPricingReady:     true,
		RegimeDetectionEnabled:  true,
		RegimeDetectorReady:     true,
	})
	if err == nil {
		t.Fatal("expected paper readiness validation to fail without paper broker")
	}
}

func TestValidateRuntimeReadinessRequiresExplicitRiskTokenForLiveMode(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                    runtimeModeLive,
		DBReady:                 true,
		BrokerExecutionRequired: true,
		BrokerConnected:         true,
		BrokerPaper:             false,
		MarketStateConfigured:   true,
		StartupPricingReady:     true,
		EarningsUniverseReady:   true,
		RegimeDetectionEnabled:  true,
		RegimeDetectorReady:     true,
		RiskTokenConfigured:     false,
	})
	if err == nil {
		t.Fatal("expected live readiness validation to fail without explicit risk token")
	}
}

func TestValidateRuntimeReadinessRejectsBrokerBackedMarketStateInPaperMode(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                    runtimeModePaper,
		DBReady:                 true,
		BrokerExecutionRequired: true,
		BrokerConnected:         true,
		BrokerPaper:             true,
		MarketStateConfigured:   true,
		MarketStateBrokerBacked: true,
		StartupPricingReady:     true,
		RegimeDetectionEnabled:  true,
		RegimeDetectorReady:     true,
	})
	if err == nil {
		t.Fatal("expected paper readiness validation to reject broker-backed market state")
	}
}

func TestValidateRuntimeReadinessAllowsKalshiOnlyWithoutBroker(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                    runtimeModePaper,
		DBReady:                 true,
		BrokerExecutionRequired: false,
		KalshiExecutionRequired: true,
		KalshiExecutionReady:    true,
		BrokerConnected:         false,
		BrokerPaper:             false,
		MarketStateConfigured:   false,
		StartupPricingReady:     false,
		RegimeDetectionEnabled:  false,
		RegimeDetectorReady:     false,
	})
	if err != nil {
		t.Fatalf("expected Kalshi-only paper readiness to pass without broker, got %v", err)
	}
}

func TestValidateRuntimeReadinessRequiresKalshiExecutorForKalshiDesks(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                    runtimeModePaper,
		DBReady:                 true,
		BrokerExecutionRequired: false,
		KalshiExecutionRequired: true,
		KalshiExecutionReady:    false,
	})
	if err == nil {
		t.Fatal("expected Kalshi readiness validation to fail without executor")
	}
}

func TestDeskExecutionRequirementHelpers(t *testing.T) {
	desks := []deskDef{
		{id: "kalshi-rates-a", domain: "prediction_market"},
		{id: "corp-earnings-a", domain: "corporate"},
	}
	if !desksRequireKalshiExecution(desks) {
		t.Fatal("expected Kalshi execution requirement")
	}
	if !desksRequireBrokerExecution(desks) {
		t.Fatal("expected broker execution requirement")
	}
	global := firm.NewManualEntryControl(firm.DisabledEntryPolicy("global_halt", time.Now().UTC()))
	broker := firm.NewManualEntryControl(firm.DisabledEntryPolicy("broker_halt", time.Now().UTC()))
	kalshiPolicy := entryControlForDesk(desks[0], global, broker).CurrentEntryPolicy()
	if kalshiPolicy.Reason != "global_halt" {
		t.Fatalf("expected Kalshi desk to inherit global control only, got %+v", kalshiPolicy)
	}
	brokerPolicy := entryControlForDesk(desks[1], firm.NewManualEntryControl(firm.NormalEntryPolicy(time.Now().UTC())), broker).CurrentEntryPolicy()
	if brokerPolicy.Reason != "broker_halt" {
		t.Fatalf("expected broker desk to combine global and broker controls, got %+v", brokerPolicy)
	}
}

type stubHealthBroker struct {
	connected bool
}

func (b stubHealthBroker) IsConnected() bool { return b.connected }

type stubHealthBook struct {
	status book.BrokerSyncStatus
}

func (b stubHealthBook) BrokerSyncStatus() book.BrokerSyncStatus { return b.status }

type stubHealthMarket struct {
	report marketdata.QuoteFreshnessReport
}

func (m stubHealthMarket) FreshnessReport(_ []model.Instrument, now time.Time, _ time.Duration) marketdata.QuoteFreshnessReport {
	report := m.report
	if report.AsOf.IsZero() {
		report.AsOf = now
	}
	return report
}

type stubHealthStore struct {
	err error
}

func (s stubHealthStore) Ping(context.Context) error { return s.err }

func TestRuntimeHealthDisablesEntriesWhenBrokerDisconnected(t *testing.T) {
	supervisor := newRuntimeHealthSupervisor(runtimeHealthConfig{
		Broker:          stubHealthBroker{connected: false},
		BrokerSync:      stubHealthBook{},
		MarketFreshness: stubHealthMarket{},
		RequiredQuotes:  []model.Instrument{{Symbol: "SPY", SecType: "STK", Currency: "USD"}},
	})

	policy := supervisor.EvaluateNow(time.Now().UTC())
	if policy.AllowEntries {
		t.Fatal("expected broker disconnect to disable entries")
	}
	if policy.Mode != firm.EntryModeEntriesDisabled {
		t.Fatalf("expected entries disabled mode, got %s", policy.Mode)
	}
	if policy.Reason != "broker_disconnected" {
		t.Fatalf("expected broker_disconnected reason, got %q", policy.Reason)
	}
}

func TestRuntimeHealthDisablesEntriesWhenQuotesAreStale(t *testing.T) {
	now := time.Now().UTC()
	supervisor := newRuntimeHealthSupervisor(runtimeHealthConfig{
		Broker: stubHealthBroker{connected: true},
		BrokerSync: stubHealthBook{status: book.BrokerSyncStatus{
			Connected:  true,
			LastSynced: now.Add(-30 * time.Second),
		}},
		MarketFreshness: stubHealthMarket{report: marketdata.QuoteFreshnessReport{
			Total:   2,
			Fresh:   1,
			Stale:   1,
			Missing: 0,
		}},
		RequiredQuotes: []model.Instrument{
			{Symbol: "SPY", SecType: "STK", Currency: "USD"},
			{Symbol: "QQQ", SecType: "STK", Currency: "USD"},
		},
	})

	policy := supervisor.EvaluateNow(now)
	if policy.AllowEntries {
		t.Fatal("expected stale quotes to disable entries")
	}
	if policy.Reason != "market_data_stale:1" {
		t.Fatalf("expected market_data_stale:1 reason, got %q", policy.Reason)
	}
}

func TestRuntimeHealthAllowsEntriesWhenBrokerAndQuotesAreHealthy(t *testing.T) {
	now := time.Now().UTC()
	supervisor := newRuntimeHealthSupervisor(runtimeHealthConfig{
		Broker: stubHealthBroker{connected: true},
		BrokerSync: stubHealthBook{status: book.BrokerSyncStatus{
			Connected:  true,
			LastSynced: now.Add(-30 * time.Second),
		}},
		MarketFreshness: stubHealthMarket{report: marketdata.QuoteFreshnessReport{
			Total:   2,
			Fresh:   2,
			Stale:   0,
			Missing: 0,
		}},
		RequiredQuotes: []model.Instrument{
			{Symbol: "SPY", SecType: "STK", Currency: "USD"},
			{Symbol: "QQQ", SecType: "STK", Currency: "USD"},
		},
	})

	policy := supervisor.EvaluateNow(now)
	if !policy.AllowEntries {
		t.Fatalf("expected healthy runtime to allow entries, got reason %q", policy.Reason)
	}
	if policy.Mode != firm.EntryModeNormal {
		t.Fatalf("expected normal mode, got %s", policy.Mode)
	}
}

func TestRuntimeHealthDisablesEntriesWhenPersistenceProbeFails(t *testing.T) {
	now := time.Now().UTC()
	supervisor := newRuntimeHealthSupervisor(runtimeHealthConfig{
		Broker: stubHealthBroker{connected: true},
		BrokerSync: stubHealthBook{status: book.BrokerSyncStatus{
			Connected:  true,
			LastSynced: now.Add(-30 * time.Second),
		}},
		MarketFreshness: stubHealthMarket{report: marketdata.QuoteFreshnessReport{
			Total:   1,
			Fresh:   1,
			Stale:   0,
			Missing: 0,
		}},
		PersistenceProbe: stubHealthStore{err: errors.New("postgres unavailable")},
		RequiredQuotes: []model.Instrument{
			{Symbol: "SPY", SecType: "STK", Currency: "USD"},
		},
	})

	policy := supervisor.EvaluateNow(now)
	if policy.AllowEntries {
		t.Fatal("expected persistence failure to disable entries")
	}
	if policy.Reason != "persistence_unavailable" {
		t.Fatalf("expected persistence_unavailable reason, got %q", policy.Reason)
	}
}
