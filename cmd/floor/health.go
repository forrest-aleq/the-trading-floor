package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
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
	Broker                    brokerConnectivity
	BrokerStatus              brokerStatusSource
	BrokerSync                brokerSyncSource
	MarketFreshness           marketFreshnessSource
	PersistenceProbe          persistenceProbe
	RequiredQuotes            []model.Instrument
	MinFreshQuotes            int
	DisableQuoteGate          bool
	Interval                  time.Duration
	MaxBrokerSyncAge          time.Duration
	MaxQuoteAge               time.Duration
	PersistenceProbeTimeout   time.Duration
	BrokerAckFailureThreshold int
	BrokerAckFailureWindow    time.Duration
	BrokerAckFailureCooldown  time.Duration
	OnPolicyChange            func(firm.EntryPolicy, map[string]any)
}

type runtimeHealthSupervisor struct {
	log                       *slog.Logger
	broker                    brokerConnectivity
	brokerStatus              brokerStatusSource
	brokerSync                brokerSyncSource
	marketFreshness           marketFreshnessSource
	persistenceProbe          persistenceProbe
	requiredQuotes            []model.Instrument
	minFreshQuotes            int
	disableQuoteGate          bool
	interval                  time.Duration
	maxBrokerSyncAge          time.Duration
	maxQuoteAge               time.Duration
	persistenceProbeTimeout   time.Duration
	brokerAckFailureThreshold int
	brokerAckFailureWindow    time.Duration
	brokerAckFailureCooldown  time.Duration
	onPolicyChange            func(firm.EntryPolicy, map[string]any)

	mu                     sync.RWMutex
	policy                 firm.EntryPolicy
	brokerAckFailures      []execution.BrokerOrderFailure
	brokerAckLastFailure   execution.BrokerOrderFailure
	brokerAckCooldownUntil time.Time
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
	if cfg.BrokerAckFailureWindow <= 0 {
		cfg.BrokerAckFailureWindow = 2 * time.Minute
	}
	if cfg.BrokerAckFailureCooldown <= 0 {
		cfg.BrokerAckFailureCooldown = 5 * time.Minute
	}
	if cfg.BrokerAckFailureThreshold < 0 {
		cfg.BrokerAckFailureThreshold = 0
	}

	supervisor := &runtimeHealthSupervisor{
		log:                       slog.Default().With("component", "runtime_health"),
		broker:                    cfg.Broker,
		brokerStatus:              cfg.BrokerStatus,
		brokerSync:                cfg.BrokerSync,
		marketFreshness:           cfg.MarketFreshness,
		persistenceProbe:          cfg.PersistenceProbe,
		requiredQuotes:            append([]model.Instrument(nil), cfg.RequiredQuotes...),
		minFreshQuotes:            normalizeMinFreshQuotes(cfg.MinFreshQuotes, len(cfg.RequiredQuotes)),
		disableQuoteGate:          cfg.DisableQuoteGate,
		interval:                  interval,
		maxBrokerSyncAge:          cfg.MaxBrokerSyncAge,
		maxQuoteAge:               cfg.MaxQuoteAge,
		persistenceProbeTimeout:   cfg.PersistenceProbeTimeout,
		brokerAckFailureThreshold: cfg.BrokerAckFailureThreshold,
		brokerAckFailureWindow:    cfg.BrokerAckFailureWindow,
		brokerAckFailureCooldown:  cfg.BrokerAckFailureCooldown,
		onPolicyChange:            cfg.OnPolicyChange,
		policy:                    firm.DisabledEntryPolicy("runtime_health_initializing", time.Now().UTC()),
	}
	return supervisor
}

func normalizeMinFreshQuotes(minFreshQuotes int, requiredQuotes int) int {
	if requiredQuotes <= 0 {
		return 0
	}
	if minFreshQuotes <= 0 || minFreshQuotes > requiredQuotes {
		return requiredQuotes
	}
	return minFreshQuotes
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

func (s *runtimeHealthSupervisor) RecordBrokerOrderFailure(failure execution.BrokerOrderFailure) {
	if s == nil || !failure.Unacknowledged {
		return
	}
	if failure.At.IsZero() {
		failure.At = time.Now().UTC()
	}

	s.mu.Lock()
	s.pruneBrokerAckFailuresLocked(failure.At)
	s.brokerAckFailures = append(s.brokerAckFailures, failure)
	s.brokerAckLastFailure = failure
	count := len(s.brokerAckFailures)
	threshold := s.brokerAckFailureThreshold
	window := s.brokerAckFailureWindow
	cooldown := s.brokerAckFailureCooldown
	var cooldownUntil time.Time
	if threshold > 0 && count >= threshold && cooldown > 0 {
		cooldownUntil = failure.At.Add(cooldown)
		if cooldownUntil.After(s.brokerAckCooldownUntil) {
			s.brokerAckCooldownUntil = cooldownUntil
		}
	}
	if !s.brokerAckCooldownUntil.IsZero() {
		cooldownUntil = s.brokerAckCooldownUntil
	}
	s.mu.Unlock()

	s.log.Error("broker order acknowledgement failure recorded",
		"desk_id", failure.Order.DeskID,
		"symbol", failure.Order.DisplaySymbol(),
		"broker_order_id", failure.BrokerOrderID,
		"status", failure.Status,
		"cause", failure.Cause,
		"hint", failure.Hint,
		"rolling_failures", count,
		"threshold", threshold,
		"window", window,
		"cooldown", cooldown,
		"cooldown_until", cooldownUntil,
		"error", failure.Error,
	)
}

func (s *runtimeHealthSupervisor) BrokerAckFailureTelemetry(now time.Time) map[string]any {
	if s == nil {
		return nil
	}
	snapshot := s.brokerAckFailureSnapshot(now)
	return map[string]any{
		"broker_ack_failure_count":           snapshot.Count,
		"broker_ack_failure_threshold":       snapshot.Threshold,
		"broker_ack_failure_window":          snapshot.Window.String(),
		"broker_ack_failure_cooldown":        snapshot.Cooldown.String(),
		"broker_ack_failure_cooldown_until":  snapshot.CooldownUntil,
		"broker_ack_failure_cooldown_active": snapshot.CooldownActive,
		"broker_ack_failure_last_at":         snapshot.LastAt,
		"broker_ack_failure_last_order_id":   snapshot.LastOrderID,
		"broker_ack_failure_last_symbol":     snapshot.LastSymbol,
		"broker_ack_failure_last_cause":      snapshot.LastCause,
		"broker_ack_failure_last_hint":       snapshot.LastHint,
	}
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

	ackFailures := s.brokerAckFailureSnapshot(now)
	details["broker_ack_failure_threshold"] = ackFailures.Threshold
	details["broker_ack_failure_window"] = ackFailures.Window.String()
	details["broker_ack_failure_cooldown"] = ackFailures.Cooldown.String()
	details["broker_ack_failure_count"] = ackFailures.Count
	if !ackFailures.CooldownUntil.IsZero() {
		details["broker_ack_failure_cooldown_until"] = ackFailures.CooldownUntil
		details["broker_ack_failure_cooldown_active"] = ackFailures.CooldownActive
	}
	if !ackFailures.LastAt.IsZero() {
		details["broker_ack_failure_last_at"] = ackFailures.LastAt
		details["broker_ack_failure_last_symbol"] = ackFailures.LastSymbol
		details["broker_ack_failure_last_order_id"] = ackFailures.LastOrderID
		details["broker_ack_failure_last_cause"] = ackFailures.LastCause
		details["broker_ack_failure_last_hint"] = ackFailures.LastHint
		details["broker_ack_failure_last_error"] = ackFailures.LastError
	}
	if ackFailures.Threshold > 0 {
		if ackFailures.CooldownActive {
			effectiveCount := ackFailures.Count
			if effectiveCount < ackFailures.Threshold {
				effectiveCount = ackFailures.Threshold
			}
			return firm.DisabledEntryPolicy(fmt.Sprintf("broker_order_ack_failures:%d/%s", effectiveCount, ackFailures.Window.Round(time.Second)), now), details
		}
		if ackFailures.Count >= ackFailures.Threshold && ackFailures.Cooldown <= 0 {
			return firm.DisabledEntryPolicy(fmt.Sprintf("broker_order_ack_failures:%d/%s", ackFailures.Count, ackFailures.Window.Round(time.Second)), now), details
		}
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
		if s.disableQuoteGate {
			details["market_data_quote_gate_disabled"] = true
			return firm.NormalEntryPolicy(now), details
		}
		return firm.DisabledEntryPolicy("market_data_unavailable", now), details
	}
	freshness := s.marketFreshness.FreshnessReport(s.requiredQuotes, now, s.maxQuoteAge)
	details["required_quotes"] = freshness.Total
	details["min_fresh_quotes"] = s.minFreshQuotes
	details["market_data_quote_gate_disabled"] = s.disableQuoteGate
	details["quote_fresh"] = freshness.Fresh
	details["quote_stale"] = freshness.Stale
	details["quote_missing"] = freshness.Missing
	details["quote_newest_age"] = freshness.NewestAge.String()
	details["quote_oldest_age"] = freshness.OldestAge.String()
	if len(freshness.MissingSymbols) > 0 {
		details["quote_missing_symbols"] = freshness.MissingSymbols
	}
	if len(freshness.StaleSymbols) > 0 {
		details["quote_stale_symbols"] = freshness.StaleSymbols
	}

	if s.disableQuoteGate {
		return firm.NormalEntryPolicy(now), details
	}
	if s.minFreshQuotes < freshness.Total && freshness.Fresh >= s.minFreshQuotes {
		return firm.NormalEntryPolicy(now), details
	}
	switch {
	case freshness.Missing > 0:
		return firm.DisabledEntryPolicy(fmt.Sprintf("market_data_missing:%d", freshness.Missing), now), details
	case freshness.Stale > 0:
		return firm.DisabledEntryPolicy(fmt.Sprintf("market_data_stale:%d", freshness.Stale), now), details
	default:
		return firm.NormalEntryPolicy(now), details
	}
}

type brokerAckFailureSnapshot struct {
	Count          int
	Threshold      int
	Window         time.Duration
	Cooldown       time.Duration
	CooldownUntil  time.Time
	CooldownActive bool
	LastAt         time.Time
	LastOrderID    int64
	LastSymbol     string
	LastCause      string
	LastHint       string
	LastError      string
}

func (s *runtimeHealthSupervisor) brokerAckFailureSnapshot(now time.Time) brokerAckFailureSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneBrokerAckFailuresLocked(now)
	if !s.brokerAckCooldownUntil.IsZero() && !now.Before(s.brokerAckCooldownUntil) {
		s.brokerAckCooldownUntil = time.Time{}
	}

	snapshot := brokerAckFailureSnapshot{
		Count:          len(s.brokerAckFailures),
		Threshold:      s.brokerAckFailureThreshold,
		Window:         s.brokerAckFailureWindow,
		Cooldown:       s.brokerAckFailureCooldown,
		CooldownUntil:  s.brokerAckCooldownUntil,
		CooldownActive: !s.brokerAckCooldownUntil.IsZero() && now.Before(s.brokerAckCooldownUntil),
	}
	if snapshot.Count == 0 && !snapshot.CooldownActive {
		return snapshot
	}
	last := s.brokerAckLastFailure
	if snapshot.Count > 0 {
		last = s.brokerAckFailures[snapshot.Count-1]
	}
	if last.At.IsZero() {
		return snapshot
	}
	snapshot.LastAt = last.At
	snapshot.LastOrderID = last.BrokerOrderID
	snapshot.LastSymbol = last.Order.DisplaySymbol()
	snapshot.LastCause = last.Cause
	snapshot.LastHint = last.Hint
	snapshot.LastError = last.Error
	return snapshot
}

func (s *runtimeHealthSupervisor) pruneBrokerAckFailuresLocked(now time.Time) {
	if s == nil || s.brokerAckFailureWindow <= 0 || len(s.brokerAckFailures) == 0 {
		return
	}
	cutoff := now.Add(-s.brokerAckFailureWindow)
	keepFrom := 0
	for keepFrom < len(s.brokerAckFailures) {
		at := s.brokerAckFailures[keepFrom].At
		if at.IsZero() || !at.Before(cutoff) {
			break
		}
		keepFrom++
	}
	if keepFrom == 0 {
		return
	}
	copy(s.brokerAckFailures, s.brokerAckFailures[keepFrom:])
	s.brokerAckFailures = s.brokerAckFailures[:len(s.brokerAckFailures)-keepFrom]
}
