package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/book"
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

type marketFreshnessSource interface {
	FreshnessReport([]model.Instrument, time.Time, time.Duration) marketdata.QuoteFreshnessReport
}

type runtimeHealthConfig struct {
	Broker           brokerConnectivity
	BrokerSync       brokerSyncSource
	MarketFreshness  marketFreshnessSource
	RequiredQuotes   []model.Instrument
	Interval         time.Duration
	MaxBrokerSyncAge time.Duration
	MaxQuoteAge      time.Duration
	OnPolicyChange   func(firm.EntryPolicy, map[string]any)
}

type runtimeHealthSupervisor struct {
	log              *slog.Logger
	broker           brokerConnectivity
	brokerSync       brokerSyncSource
	marketFreshness  marketFreshnessSource
	requiredQuotes   []model.Instrument
	interval         time.Duration
	maxBrokerSyncAge time.Duration
	maxQuoteAge      time.Duration
	onPolicyChange   func(firm.EntryPolicy, map[string]any)

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

	supervisor := &runtimeHealthSupervisor{
		log:              slog.Default().With("component", "runtime_health"),
		broker:           cfg.Broker,
		brokerSync:       cfg.BrokerSync,
		marketFreshness:  cfg.MarketFreshness,
		requiredQuotes:   append([]model.Instrument(nil), cfg.RequiredQuotes...),
		interval:         interval,
		maxBrokerSyncAge: cfg.MaxBrokerSyncAge,
		maxQuoteAge:      cfg.MaxQuoteAge,
		onPolicyChange:   cfg.OnPolicyChange,
		policy:           firm.DisabledEntryPolicy("runtime_health_initializing", time.Now().UTC()),
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
