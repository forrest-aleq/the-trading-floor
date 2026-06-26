package book

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

type ThesisLookup func(string) (*model.Thesis, bool)

// Monitor watches open positions for thesis-defined exits.
type Monitor struct {
	log          *slog.Logger
	book         *Book
	thesisLookup ThesisLookup
	onClose      func(position *model.Position, exitPrice float64, reason string)
	onLifecycle  func(position *model.Position, alert model.LifecycleAlert)
	interval     time.Duration
	now          func() time.Time

	staleMarkMaxAge        time.Duration
	emergencyLossPct       float64
	optionEmergencyLossPct float64
	exitRetryCooldown      time.Duration

	// markHealth tracks per-position mark state so alerts fire on the
	// transition into ill health, not on every 10-second cycle. Accessed
	// only from RunOnce, which is single-threaded.
	markHealth    map[string]string
	closeAttempts map[string]closeAttempt
}

type closeAttempt struct {
	reason      string
	attemptedAt time.Time
}

func NewMonitor(book *Book, thesisLookup ThesisLookup, onClose func(*model.Position, float64, string)) *Monitor {
	return &Monitor{
		log:                    slog.Default().With("component", "monitor"),
		book:                   book,
		thesisLookup:           thesisLookup,
		onClose:                onClose,
		interval:               10 * time.Second,
		now:                    time.Now,
		staleMarkMaxAge:        2 * time.Minute,
		emergencyLossPct:       0.05,
		optionEmergencyLossPct: 0.50,
		exitRetryCooldown:      5 * time.Minute,
		markHealth:             make(map[string]string),
		closeAttempts:          make(map[string]closeAttempt),
	}
}

// SetStaleMarkMaxAge sets how old a position's mark may be before the monitor
// raises a stale_mark alert. Zero disables the check.
func (m *Monitor) SetStaleMarkMaxAge(age time.Duration) {
	if age >= 0 {
		m.staleMarkMaxAge = age
	}
}

func (m *Monitor) SetEmergencyLossThresholds(stockPct, optionPct float64) {
	if stockPct > 0 {
		m.emergencyLossPct = stockPct
	}
	if optionPct > 0 {
		m.optionEmergencyLossPct = optionPct
	}
}

func (m *Monitor) SetInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	m.interval = interval
}

func (m *Monitor) SetExitRetryCooldown(cooldown time.Duration) {
	if cooldown >= 0 {
		m.exitRetryCooldown = cooldown
	}
}

func (m *Monitor) SetLifecycleHandler(handler func(position *model.Position, alert model.LifecycleAlert)) {
	m.onLifecycle = handler
}

func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.RunOnce()
		}
	}
}

func (m *Monitor) RunOnce() {
	positions := m.book.GetOpenPositions()
	now := m.currentTime()
	seen := make(map[string]bool, len(positions))
	for _, pos := range positions {
		seen[pos.ID] = true
		if pos.CurrentPrice <= 0 {
			if m.markHealthBecame(pos.ID, "missing_mark") {
				m.raiseMarkAlert(pos, "missing_mark", "position has no usable mark; exit checks suspended")
			}
			continue
		}
		stale := false
		if m.staleMarkMaxAge > 0 {
			markedAt := pos.MarkedAt
			if markedAt.IsZero() {
				markedAt = pos.OpenedAt
			}
			if age := now.Sub(markedAt); age > m.staleMarkMaxAge {
				stale = true
				if m.markHealthBecame(pos.ID, "stale_mark") {
					m.raiseMarkAlert(pos, "stale_mark",
						fmt.Sprintf("mark is %s old; exit checks are running on a stale price", age.Round(time.Second)))
				}
			}
		}
		if !stale {
			m.markHealthRecovered(pos)
		}

		thesis, _ := m.lookup(pos.ThesisID)
		if thesis != nil {
			lifecycleAlerts := detectLifecycleAlerts(pos, now)
			for _, alert := range lifecycleAlerts {
				m.log.Warn("position lifecycle alert",
					"symbol", pos.DisplaySymbol(),
					"desk", pos.DeskID,
					"kind", alert.Kind,
					"severity", alert.Severity,
					"message", alert.Message,
				)
				if m.onLifecycle != nil {
					m.onLifecycle(pos, alert)
				}
			}
			if reason, closeNow := shouldCloseOnLifecycle(lifecycleAlerts); closeNow {
				m.requestClose(pos, pos.CurrentPrice, reason)
				continue
			}
			if shouldCloseOnStop(pos, thesis) {
				m.requestClose(pos, pos.CurrentPrice, "stop_loss")
				continue
			}
			if shouldCloseOnTarget(pos, thesis) {
				m.requestClose(pos, pos.CurrentPrice, "target_hit")
				continue
			}
			if thesis.TimeHorizon > 0 && now.Sub(pos.OpenedAt) >= thesis.TimeHorizon {
				m.requestClose(pos, pos.CurrentPrice, "time_horizon")
				continue
			}
		}

		if m.emergencyLossHit(pos) {
			m.requestClose(pos, pos.CurrentPrice, "emergency_loss_backstop")
		}
	}

	for id := range m.markHealth {
		if !seen[id] {
			delete(m.markHealth, id)
		}
	}
	for id := range m.closeAttempts {
		if !seen[id] {
			delete(m.closeAttempts, id)
		}
	}
}

func (m *Monitor) markHealthBecame(positionID, state string) bool {
	prev := m.markHealth[positionID]
	m.markHealth[positionID] = state
	return prev != state
}

func (m *Monitor) markHealthRecovered(pos *model.Position) {
	if prev, ok := m.markHealth[pos.ID]; ok && prev != "" {
		m.log.Info("position mark recovered",
			"symbol", pos.DisplaySymbol(),
			"desk", pos.DeskID,
			"previous_state", prev,
		)
		delete(m.markHealth, pos.ID)
	}
}

func (m *Monitor) currentTime() time.Time {
	if m != nil && m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Monitor) lookup(thesisID string) (*model.Thesis, bool) {
	if m.thesisLookup == nil {
		return nil, false
	}
	return m.thesisLookup(thesisID)
}

func (m *Monitor) requestClose(pos *model.Position, exitPrice float64, reason string) {
	if pos == nil {
		return
	}
	now := m.currentTime()
	if m.exitRetryCooldown > 0 {
		if attempt, ok := m.closeAttempts[pos.ID]; ok {
			retryAt := attempt.attemptedAt.Add(m.exitRetryCooldown)
			if now.Before(retryAt) {
				m.log.Warn("position exit already attempted; suppressing duplicate close",
					"symbol", pos.DisplaySymbol(),
					"desk", pos.DeskID,
					"reason", reason,
					"previous_reason", attempt.reason,
					"retry_at", retryAt,
				)
				return
			}
		}
		m.closeAttempts[pos.ID] = closeAttempt{
			reason:      reason,
			attemptedAt: now,
		}
	} else {
		if _, ok := m.closeAttempts[pos.ID]; ok {
			delete(m.closeAttempts, pos.ID)
		}
	}

	m.log.Warn("position exit triggered",
		"symbol", pos.DisplaySymbol(),
		"desk", pos.DeskID,
		"reason", reason,
		"exit_price", exitPrice,
	)

	if m.onClose != nil {
		m.onClose(pos, exitPrice, reason)
	}
}

func shouldCloseOnStop(pos *model.Position, thesis *model.Thesis) bool {
	if thesis == nil || thesis.StopLoss <= 0 {
		return false
	}

	if pos.Direction == model.Long {
		return pos.CurrentPrice <= thesis.StopLoss
	}
	return pos.CurrentPrice >= thesis.StopLoss
}

func shouldCloseOnTarget(pos *model.Position, thesis *model.Thesis) bool {
	if thesis == nil || thesis.TargetPrice <= 0 {
		return false
	}

	if pos.Direction == model.Long {
		return pos.CurrentPrice >= thesis.TargetPrice
	}
	return pos.CurrentPrice <= thesis.TargetPrice
}

func (m *Monitor) raiseMarkAlert(pos *model.Position, kind, message string) {
	m.log.Warn("position mark health alert",
		"symbol", pos.DisplaySymbol(),
		"desk", pos.DeskID,
		"kind", kind,
		"message", message,
	)
	if m.onLifecycle != nil {
		m.onLifecycle(pos, model.LifecycleAlert{
			Kind:       kind,
			Severity:   "critical",
			Message:    message,
			Instrument: pos.DisplaySymbol(),
		})
	}
}

func (m *Monitor) emergencyLossHit(pos *model.Position) bool {
	if pos.EntryPrice <= 0 {
		return false
	}
	lossPct := 0.0
	if pos.Direction == model.Long {
		lossPct = (pos.EntryPrice - pos.CurrentPrice) / pos.EntryPrice
	} else {
		lossPct = (pos.CurrentPrice - pos.EntryPrice) / pos.EntryPrice
	}
	threshold := m.emergencyLossPct
	if positionHoldsOptions(pos) {
		// Option premiums routinely swing more than the equity backstop
		// tolerates; a 5% premium move is noise, not an emergency.
		threshold = m.optionEmergencyLossPct
	}
	return threshold > 0 && lossPct > threshold
}

func positionHoldsOptions(pos *model.Position) bool {
	for _, inst := range pos.ExecutionInstruments() {
		switch strings.ToUpper(inst.SecType) {
		case "OPT", "FOP":
			return true
		}
	}
	return false
}

func detectLifecycleAlerts(pos *model.Position, now time.Time) []model.LifecycleAlert {
	instruments := pos.ExecutionInstruments()
	alerts := make([]model.LifecycleAlert, 0, 2)
	for i, instrument := range instruments {
		expiry, ok := parseInstrumentExpiry(instrument.Expiry)
		if !ok {
			continue
		}
		hoursToExpiry := expiry.Sub(now).Hours()
		if hoursToExpiry <= 0 {
			alerts = append(alerts, model.LifecycleAlert{
				Kind:       "expired_derivative",
				Severity:   "critical",
				Message:    "derivative contract has expired or rolled through expiry",
				Instrument: instrument.Label(),
				ExpiresAt:  expiry,
			})
			continue
		}
		if instrument.SecType == "OPT" || instrument.SecType == "FOP" {
			if isShortOptionLeg(pos, i) && hoursToExpiry <= 24 {
				alerts = append(alerts, model.LifecycleAlert{
					Kind:       "assignment_risk",
					Severity:   "high",
					Message:    "short option is inside the assignment window",
					Instrument: instrument.Label(),
					ExpiresAt:  expiry,
				})
			}
			if pos.IsMultiLeg() && hoursToExpiry <= 48 {
				alerts = append(alerts, model.LifecycleAlert{
					Kind:       "pin_risk",
					Severity:   "high",
					Message:    "multi-leg option structure is near expiry and may pin across strikes",
					Instrument: instrument.Label(),
					ExpiresAt:  expiry,
				})
			}
		}
		if hoursToExpiry <= 24 {
			alerts = append(alerts, model.LifecycleAlert{
				Kind:       "expiry_management",
				Severity:   "medium",
				Message:    "position is inside the expiry management window",
				Instrument: instrument.Label(),
				ExpiresAt:  expiry,
			})
		}
	}
	return alerts
}

func shouldCloseOnLifecycle(alerts []model.LifecycleAlert) (string, bool) {
	for _, alert := range alerts {
		switch alert.Kind {
		case "expired_derivative", "assignment_risk", "pin_risk":
			return alert.Kind, true
		}
	}
	return "", false
}

func parseInstrumentExpiry(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}

	for _, layout := range []string{"20060102", "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return expirationSessionClose(parsed), true
		}
	}

	if parsed, err := time.Parse("200601", raw); err == nil {
		year, month, _ := parsed.Date()
		lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC)
		return expirationSessionClose(lastDay), true
	}

	return time.Time{}, false
}

func isShortOptionLeg(pos *model.Position, idx int) bool {
	if len(pos.Legs) == 0 {
		return pos.Direction == model.Short && (pos.Instrument.SecType == "OPT" || pos.Instrument.SecType == "FOP")
	}
	if idx < 0 || idx >= len(pos.Legs) {
		return false
	}
	leg := pos.Legs[idx]
	return leg.Direction == model.Short && (leg.Instrument.SecType == "OPT" || leg.Instrument.SecType == "FOP")
}

func expirationSessionClose(date time.Time) time.Time {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.FixedZone("America/New_York", -5*60*60)
	}

	year, month, day := date.Date()
	return time.Date(year, month, day, 16, 0, 0, 0, loc).UTC()
}
