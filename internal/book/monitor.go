package book

import (
	"context"
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
}

func NewMonitor(book *Book, thesisLookup ThesisLookup, onClose func(*model.Position, float64, string)) *Monitor {
	return &Monitor{
		log:          slog.Default().With("component", "monitor"),
		book:         book,
		thesisLookup: thesisLookup,
		onClose:      onClose,
		interval:     10 * time.Second,
		now:          time.Now,
	}
}

func (m *Monitor) SetInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	m.interval = interval
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
	for _, pos := range positions {
		if pos.CurrentPrice <= 0 {
			continue
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

		if emergencyLossHit(pos) {
			m.requestClose(pos, pos.CurrentPrice, "emergency_loss_backstop")
		}
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

func emergencyLossHit(pos *model.Position) bool {
	lossPct := 0.0
	if pos.Direction == model.Long {
		lossPct = (pos.EntryPrice - pos.CurrentPrice) / pos.EntryPrice
	} else {
		lossPct = (pos.CurrentPrice - pos.EntryPrice) / pos.EntryPrice
	}
	return lossPct > 0.05
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
