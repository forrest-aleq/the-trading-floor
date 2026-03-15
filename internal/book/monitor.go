package book

import (
	"context"
	"log/slog"
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
	interval     time.Duration
}

func NewMonitor(book *Book, thesisLookup ThesisLookup, onClose func(*model.Position, float64, string)) *Monitor {
	return &Monitor{
		log:          slog.Default().With("component", "monitor"),
		book:         book,
		thesisLookup: thesisLookup,
		onClose:      onClose,
		interval:     10 * time.Second,
	}
}

func (m *Monitor) SetInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	m.interval = interval
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
	for _, pos := range positions {
		if pos.CurrentPrice <= 0 {
			continue
		}

		thesis, _ := m.lookup(pos.ThesisID)
		if thesis != nil {
			if shouldCloseOnStop(pos, thesis) {
				m.requestClose(pos, pos.CurrentPrice, "stop_loss")
				continue
			}
			if shouldCloseOnTarget(pos, thesis) {
				m.requestClose(pos, pos.CurrentPrice, "target_hit")
				continue
			}
			if thesis.TimeHorizon > 0 && time.Since(pos.OpenedAt) >= thesis.TimeHorizon {
				m.requestClose(pos, pos.CurrentPrice, "time_horizon")
				continue
			}
		}

		if emergencyLossHit(pos) {
			m.requestClose(pos, pos.CurrentPrice, "emergency_loss_backstop")
		}
	}
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
