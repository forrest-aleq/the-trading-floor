package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/marketdata"
	"github.com/hnic/trading-floor/pkg/model"
)

type brokerConnectivity interface {
	IsConnected() bool
}

type brokerSyncSource interface {
	BrokerSyncStatus() book.BrokerSyncStatus
}

type brokerStatusSource interface {
	ConnectionStatus() ibkr.ConnectionStatus
}

type marketFreshnessSource interface {
	FreshnessReport([]model.Instrument, time.Time, time.Duration) marketdata.QuoteFreshnessReport
}

type persistenceProbe interface {
	Ping(context.Context) error
}

type runtimeHealthConfig struct {
	Broker                  brokerConnectivity
	BrokerStatus            brokerStatusSource
	BrokerSync              brokerSyncSource
	MarketFreshness         marketFreshnessSource
	PersistenceProbe        persistenceProbe
	RequiredQuotes          []model.Instrument
	Interval                time.Duration
	MaxBrokerSyncAge        time.Duration
	MaxQuoteAge             time.Duration
	PersistenceProbeTimeout time.Duration
	OnPolicyChange          func(firm.EntryPolicy, map[string]any)
}

type runtimeHealthSupervisor struct {
	log                     *slog.Logger
	broker                  brokerConnectivity
	brokerStatus            brokerStatusSource
	brokerSync              brokerSyncSource
	marketFreshness         marketFreshnessSource
	persistenceProbe        persistenceProbe
	requiredQuotes          []model.Instrument
	interval                time.Duration
	maxBrokerSyncAge        time.Duration
	maxQuoteAge             time.Duration
	persistenceProbeTimeout time.Duration
	onPolicyChange          func(firm.EntryPolicy, map[string]any)

	mu     sync.RWMutex
	policy firm.EntryPolicy
}

func newRuntimeHealthSupervisor(cfg runtimeHealthConfig) *runtimeHealthSupervisor {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if cfg.MaxBrokerSyncAge <= 0 {
		cfg.MaxBrokerSyncAge = 2 * time.Minute
	}
	if cfg.MaxQuoteAge <= 0 {
		cfg.MaxQuoteAge = 2 * time.Minute
	}
	if cfg.PersistenceProbeTimeout <= 0 {
		cfg.PersistenceProbeTimeout = 2 * time.Second
	}

	supervisor := &runtimeHealthSupervisor{
		log:                     slog.Default().With("component", "runtime_health"),
		broker:                  cfg.Broker,
		brokerStatus:            cfg.BrokerStatus,
		brokerSync:              cfg.BrokerSync,
		marketFreshness:         cfg.MarketFreshness,
		persistenceProbe:        cfg.PersistenceProbe,
		requiredQuotes:          append([]model.Instrument(nil), cfg.RequiredQuotes...),
		interval:                interval,
		maxBrokerSyncAge:        cfg.MaxBrokerSyncAge,
		maxQuoteAge:             cfg.MaxQuoteAge,
		persistenceProbeTimeout: cfg.PersistenceProbeTimeout,
		onPolicyChange:          cfg.OnPolicyChange,
		policy:                  firm.DisabledEntryPolicy("runtime_health_initializing", time.Now().UTC()),
	}
	return supervisor
}

func (s *runtimeHealthSupervisor) CurrentEntryPolicy() firm.EntryPolicy {
	if s == nil {
		return firm.NormalEntryPolicy(time.Now().UTC())
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.policy.Mode == "" {
		return firm.NormalEntryPolicy(time.Now().UTC())
	}
	return s.policy
}

func (s *runtimeHealthSupervisor) EvaluateNow(now time.Time) firm.EntryPolicy {
	if s == nil {
		return firm.NormalEntryPolicy(now)
	}

	policy, details := s.evaluate(now)

	s.mu.Lock()
	changed := s.policy.Mode != policy.Mode || s.policy.AllowEntries != policy.AllowEntries || s.policy.Reason != policy.Reason
	s.policy = policy
	s.mu.Unlock()

	if changed {
		s.log.Warn("runtime entry policy changed",
			"mode", policy.Mode,
			"allow_entries", policy.AllowEntries,
			"reason", policy.Reason,
			"details", details,
		)
		if s.onPolicyChange != nil {
			s.onPolicyChange(policy, details)
		}
	}
	return policy
}

func (s *runtimeHealthSupervisor) Run(ctx context.Context) {
	if s == nil {
		<-ctx.Done()
		return
	}

	s.EvaluateNow(time.Now().UTC())

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case tick := <-ticker.C:
			s.EvaluateNow(tick.UTC())
		}
	}
}

func (s *runtimeHealthSupervisor) evaluate(now time.Time) (firm.EntryPolicy, map[string]any) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	details := map[string]any{
		"as_of": now,
	}

	if s.broker == nil {
		return firm.DisabledEntryPolicy("broker_status_unavailable", now), details
	}
	if s.brokerStatus != nil {
		status := s.brokerStatus.ConnectionStatus()
		details["broker_client_id"] = status.ClientID
		details["broker_last_connect_error"] = status.LastConnectErr
		details["broker_last_attempt_at"] = status.LastAttemptAt
		details["broker_last_connected_at"] = status.LastConnectedAt
	}
	if !s.broker.IsConnected() {
		details["broker_connected"] = false
		return firm.DisabledEntryPolicy("broker_disconnected", now), details
	}
	details["broker_connected"] = true

	if s.brokerSync == nil {
		return firm.DisabledEntryPolicy("broker_sync_unavailable", now), details
	}
	syncStatus := s.brokerSync.BrokerSyncStatus()
	details["broker_last_synced"] = syncStatus.LastSynced
	details["broker_last_account_synced"] = syncStatus.LastAccountSynced
	details["broker_last_positions_synced"] = syncStatus.LastPositionsSynced
	details["broker_last_failure"] = syncStatus.LastFailure
	details["broker_last_error"] = syncStatus.LastError
	details["broker_consecutive_failures"] = syncStatus.ConsecutiveFailures
	if syncStatus.LastSynced.IsZero() {
		return firm.DisabledEntryPolicy("broker_sync_missing", now), details
	}
	brokerAge := now.Sub(syncStatus.LastSynced.UTC())
	if brokerAge < 0 {
		brokerAge = 0
	}
	details["broker_sync_age"] = brokerAge.String()
	if s.maxBrokerSyncAge > 0 && brokerAge > s.maxBrokerSyncAge {
		return firm.DisabledEntryPolicy(fmt.Sprintf("broker_sync_stale:%s", brokerAge.Round(time.Second)), now), details
	}

	if s.persistenceProbe != nil {
		pingCtx, cancel := context.WithTimeout(context.Background(), s.persistenceProbeTimeout)
		err := s.persistenceProbe.Ping(pingCtx)
		cancel()
		details["persistence_probe_timeout"] = s.persistenceProbeTimeout.String()
		if err != nil {
			details["persistence_ready"] = false
			details["persistence_error"] = err.Error()
			return firm.DisabledEntryPolicy("persistence_unavailable", now), details
		}
		details["persistence_ready"] = true
	}

	if len(s.requiredQuotes) == 0 {
		return firm.NormalEntryPolicy(now), details
	}
	if s.marketFreshness == nil {
		return firm.DisabledEntryPolicy("market_data_unavailable", now), details
	}
	freshness := s.marketFreshness.FreshnessReport(s.requiredQuotes, now, s.maxQuoteAge)
	details["required_quotes"] = freshness.Total
	details["quote_fresh"] = freshness.Fresh
	details["quote_stale"] = freshness.Stale
	details["quote_missing"] = freshness.Missing
	details["quote_newest_age"] = freshness.NewestAge.String()
	details["quote_oldest_age"] = freshness.OldestAge.String()

	switch {
	case freshness.Missing > 0:
		return firm.DisabledEntryPolicy(fmt.Sprintf("market_data_missing:%d", freshness.Missing), now), details
	case freshness.Stale > 0:
		return firm.DisabledEntryPolicy(fmt.Sprintf("market_data_stale:%d", freshness.Stale), now), details
	default:
		return firm.NormalEntryPolicy(now), details
	}
}
