package book

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

func stalenessTestInstrument(sym, secType string) model.Instrument {
	inst := model.Instrument{Symbol: sym, SecType: secType, Currency: "USD"}
	if secType == "OPT" {
		inst.Exchange = "SMART"
		inst.Expiry = "20270115"
		inst.Strike = 100
		inst.Right = "C"
	}
	return inst
}

func openStalenessTestPosition(bk *Book, sym, secType string, entry float64) *model.Position {
	inst := stalenessTestInstrument(sym, secType)
	return bk.OpenPosition(&model.Fill{
		OrderID:    "pos-" + sym,
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   1,
		AvgPrice:   entry,
		FilledAt:   time.Now().Add(-time.Hour),
	}, &model.Thesis{
		ID:         "thesis-" + sym,
		DeskID:     "desk-a",
		Instrument: inst,
		Direction:  model.Long,
		EntryPrice: entry,
	})
}

func hasAlertKind(alerts []model.LifecycleAlert, kind string) bool {
	for _, alert := range alerts {
		if alert.Kind == kind {
			return true
		}
	}
	return false
}

func TestMonitorAlertsOnMissingMarkInsteadOfSilentSkip(t *testing.T) {
	bk := NewBook(nil, 100000)
	pos := openStalenessTestPosition(bk, "AAPL", "STK", 100)
	pos.CurrentPrice = 0

	var alerts []model.LifecycleAlert
	closeReason := ""
	monitor := NewMonitor(bk, func(string) (*model.Thesis, bool) { return nil, false },
		func(_ *model.Position, _ float64, reason string) { closeReason = reason })
	monitor.SetLifecycleHandler(func(_ *model.Position, alert model.LifecycleAlert) {
		alerts = append(alerts, alert)
	})

	monitor.RunOnce()

	if closeReason != "" {
		t.Fatalf("expected no close for unmarked position, got %q", closeReason)
	}
	if !hasAlertKind(alerts, "missing_mark") {
		t.Fatalf("expected missing_mark alert, got %+v", alerts)
	}
}

func TestMonitorAlertsOnStaleMarkButStillEnforcesStops(t *testing.T) {
	bk := NewBook(nil, 100000)
	pos := openStalenessTestPosition(bk, "MSFT", "STK", 100)
	now := time.Now()
	pos.CurrentPrice = 85
	pos.MarkedAt = now.Add(-10 * time.Minute)

	thesis := &model.Thesis{
		ID:         pos.ThesisID,
		DeskID:     "desk-a",
		Instrument: stalenessTestInstrument("MSFT", "STK"),
		Direction:  model.Long,
		EntryPrice: 100,
		StopLoss:   90,
	}

	var alerts []model.LifecycleAlert
	closeReason := ""
	monitor := NewMonitor(bk, func(id string) (*model.Thesis, bool) { return thesis, id == thesis.ID },
		func(_ *model.Position, _ float64, reason string) { closeReason = reason })
	monitor.SetLifecycleHandler(func(_ *model.Position, alert model.LifecycleAlert) {
		alerts = append(alerts, alert)
	})
	monitor.now = func() time.Time { return now }

	monitor.RunOnce()

	if !hasAlertKind(alerts, "stale_mark") {
		t.Fatalf("expected stale_mark alert for a 10m-old mark, got %+v", alerts)
	}
	if closeReason != "stop_loss" {
		t.Fatalf("expected stop_loss close to still fire on stale mark, got %q", closeReason)
	}
}

func TestEmergencyBackstopAllowsRoutineOptionDrawdown(t *testing.T) {
	bk := NewBook(nil, 100000)
	pos := openStalenessTestPosition(bk, "NVDA", "OPT", 2.00)
	now := time.Now()
	pos.CurrentPrice = 1.70 // -15% of premium: routine option noise
	pos.MarkedAt = now

	closeReason := ""
	monitor := NewMonitor(bk, func(string) (*model.Thesis, bool) { return nil, false },
		func(_ *model.Position, _ float64, reason string) { closeReason = reason })
	monitor.now = func() time.Time { return now }

	monitor.RunOnce()

	if closeReason != "" {
		t.Fatalf("expected no emergency close at -15%% premium, got %q", closeReason)
	}
}

func TestEmergencyBackstopClosesCollapsedOption(t *testing.T) {
	bk := NewBook(nil, 100000)
	pos := openStalenessTestPosition(bk, "AMD", "OPT", 2.00)
	now := time.Now()
	pos.CurrentPrice = 0.80 // -60% of premium
	pos.MarkedAt = now

	closeReason := ""
	monitor := NewMonitor(bk, func(string) (*model.Thesis, bool) { return nil, false },
		func(_ *model.Position, _ float64, reason string) { closeReason = reason })
	monitor.now = func() time.Time { return now }

	monitor.RunOnce()

	if closeReason != "emergency_loss_backstop" {
		t.Fatalf("expected emergency close at -60%% premium, got %q", closeReason)
	}
}

// A stale position must not page the operator every 10-second cycle all
// night; the alert fires on the transition into staleness and again only
// after recovering and going stale anew.
func TestMarkAlertsFireOncePerEpisode(t *testing.T) {
	bk := NewBook(nil, 100000)
	pos := openStalenessTestPosition(bk, "META", "STK", 100)
	now := time.Now()
	pos.CurrentPrice = 100
	pos.MarkedAt = now.Add(-10 * time.Minute)

	var alerts []model.LifecycleAlert
	monitor := NewMonitor(bk, func(string) (*model.Thesis, bool) { return nil, false },
		func(_ *model.Position, _ float64, _ string) {})
	monitor.SetLifecycleHandler(func(_ *model.Position, alert model.LifecycleAlert) {
		alerts = append(alerts, alert)
	})
	monitor.now = func() time.Time { return now }

	monitor.RunOnce()
	monitor.RunOnce()
	if got := countAlertKind(alerts, "stale_mark"); got != 1 {
		t.Fatalf("expected exactly one stale_mark alert across repeated cycles, got %d", got)
	}

	pos.MarkedAt = now // mark recovers
	monitor.RunOnce()
	if got := countAlertKind(alerts, "stale_mark"); got != 1 {
		t.Fatalf("expected no alert while healthy, got %d", got)
	}

	pos.MarkedAt = now.Add(-10 * time.Minute) // goes stale again
	monitor.RunOnce()
	if got := countAlertKind(alerts, "stale_mark"); got != 2 {
		t.Fatalf("expected second alert for new staleness episode, got %d", got)
	}
}

func countAlertKind(alerts []model.LifecycleAlert, kind string) int {
	count := 0
	for _, alert := range alerts {
		if alert.Kind == kind {
			count++
		}
	}
	return count
}

func TestEmergencyBackstopStockThresholdUnchanged(t *testing.T) {
	bk := NewBook(nil, 100000)
	pos := openStalenessTestPosition(bk, "TSLA", "STK", 100)
	now := time.Now()
	pos.CurrentPrice = 94 // -6%
	pos.MarkedAt = now

	closeReason := ""
	monitor := NewMonitor(bk, func(string) (*model.Thesis, bool) { return nil, false },
		func(_ *model.Position, _ float64, reason string) { closeReason = reason })
	monitor.now = func() time.Time { return now }

	monitor.RunOnce()

	if closeReason != "emergency_loss_backstop" {
		t.Fatalf("expected emergency close at -6%% on stock, got %q", closeReason)
	}
}

func TestMonitorSuppressesDuplicateCloseAttemptsUntilCooldown(t *testing.T) {
	bk := NewBook(nil, 100000)
	pos := openStalenessTestPosition(bk, "BB", "STK", 100)
	now := time.Date(2026, 6, 26, 20, 0, 0, 0, time.UTC)
	pos.CurrentPrice = 90
	pos.MarkedAt = now

	closeCount := 0
	monitor := NewMonitor(bk, func(string) (*model.Thesis, bool) { return nil, false },
		func(_ *model.Position, _ float64, reason string) {
			closeCount++
			if reason != "emergency_loss_backstop" {
				t.Fatalf("unexpected close reason %q", reason)
			}
		})
	monitor.now = func() time.Time { return now }
	monitor.SetStaleMarkMaxAge(0)
	monitor.SetExitRetryCooldown(5 * time.Minute)

	monitor.RunOnce()
	monitor.RunOnce()
	if closeCount != 1 {
		t.Fatalf("expected one close attempt before cooldown, got %d", closeCount)
	}

	now = now.Add(4 * time.Minute)
	monitor.RunOnce()
	if closeCount != 1 {
		t.Fatalf("expected duplicate close suppressed inside cooldown, got %d", closeCount)
	}

	now = now.Add(2 * time.Minute)
	monitor.RunOnce()
	if closeCount != 2 {
		t.Fatalf("expected retry after cooldown, got %d", closeCount)
	}
}

func TestMonitorCloseAttemptUsesCurrentCooldown(t *testing.T) {
	bk := NewBook(nil, 100000)
	pos := openStalenessTestPosition(bk, "IBM", "STK", 100)
	now := time.Date(2026, 6, 26, 20, 0, 0, 0, time.UTC)
	pos.CurrentPrice = 90
	pos.MarkedAt = now

	closeCount := 0
	monitor := NewMonitor(bk, func(string) (*model.Thesis, bool) { return nil, false },
		func(_ *model.Position, _ float64, _ string) { closeCount++ })
	monitor.now = func() time.Time { return now }
	monitor.SetStaleMarkMaxAge(0)
	monitor.SetExitRetryCooldown(5 * time.Minute)

	monitor.RunOnce()
	monitor.SetExitRetryCooldown(time.Minute)
	now = now.Add(2 * time.Minute)
	monitor.RunOnce()

	if closeCount != 2 {
		t.Fatalf("expected updated cooldown to allow retry, got %d close attempts", closeCount)
	}
}
