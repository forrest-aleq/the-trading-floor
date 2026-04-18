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

func TestRegisterDefaultFeedsRegistersExtendedWireSurface(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")
	t.Setenv("FMP_API_KEY", "")
	t.Setenv("EARNINGS_API_KEY", "")
	t.Setenv("TELEGRAM_FEED_URLS", "")
	t.Setenv("ALT_DATA_SOURCES", "")

	wireMgr := wire.NewManager()
	count := registerDefaultFeeds(wireMgr, stubMarketStateProvider{})
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

func TestValidateRuntimeReadinessRequiresPaperBrokerForPaperMode(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                   runtimeModePaper,
		DBReady:                true,
		BrokerConnected:        true,
		BrokerPaper:            false,
		MarketStateConfigured:  true,
		StartupPricingReady:    true,
		RegimeDetectionEnabled: true,
		RegimeDetectorReady:    true,
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
		MarketStateConfigured:  true,
		StartupPricingReady:    true,
		EarningsUniverseReady:  true,
		RegimeDetectionEnabled: true,
		RegimeDetectorReady:    true,
		RiskTokenConfigured:    false,
	})
	if err == nil {
		t.Fatal("expected live readiness validation to fail without explicit risk token")
	}
}

func TestValidateRuntimeReadinessRejectsBrokerBackedMarketStateInPaperMode(t *testing.T) {
	err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                    runtimeModePaper,
		DBReady:                 true,
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
