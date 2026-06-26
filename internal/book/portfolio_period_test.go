package book

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

func openPeriodTestPosition(bk *Book, orderID string) {
	inst := model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"}
	bk.OpenPosition(&model.Fill{
		OrderID:    orderID,
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   100,
		AvgPrice:   100,
		FilledAt:   bk.currentTime(),
	}, &model.Thesis{
		ID:         "thesis-" + orderID,
		DeskID:     "desk-1",
		Instrument: inst,
		Direction:  model.Long,
	})
}

func TestSnapshotReportsCurrentDrawdownFromPeak(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	bk.SetDeskCapital("desk-1", 100000)
	openPeriodTestPosition(bk, "order-dd")

	bk.Mark(map[string]float64{"AAPL": 300}) // NAV 120k = peak
	bk.Mark(map[string]float64{"AAPL": 50})  // NAV 95k → 20.83% off peak

	snap := bk.Snapshot()
	if snap.CurrentDrawdownPct < 20.5 || snap.CurrentDrawdownPct > 21.2 {
		t.Fatalf("expected ~20.8%% current drawdown, got %.2f", snap.CurrentDrawdownPct)
	}
	if snap.MaxDrawdown < 0.205 || snap.MaxDrawdown > 0.212 {
		t.Fatalf("expected max drawdown fraction ~0.208, got %.4f", snap.MaxDrawdown)
	}
}

func TestBrokerNAVDrivesPeakAndMaxDrawdown(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)

	bk.applyAccountSummary(&ibkr.AccountSummary{NetLiquidation: 130000, Cash: 130000})
	bk.applyAccountSummary(&ibkr.AccountSummary{NetLiquidation: 110000, Cash: 110000})
	bk.markBrokerSyncHealthy()

	snap := bk.Snapshot()
	// peak 130k → 110k is a 15.38% drawdown, even though the local book never moved
	if snap.MaxDrawdown < 0.150 || snap.MaxDrawdown > 0.158 {
		t.Fatalf("expected broker NAV to drive max drawdown ~0.154, got %.4f", snap.MaxDrawdown)
	}
	if snap.CurrentDrawdownPct < 15.0 || snap.CurrentDrawdownPct > 15.8 {
		t.Fatalf("expected ~15.4%% current drawdown from broker NAV, got %.2f", snap.CurrentDrawdownPct)
	}
}

func TestPeriodPnLTracksCalendarWindows(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	bk.SetDeskCapital("desk-1", 100000)
	clock := time.Date(2026, time.May, 20, 10, 0, 0, 0, time.UTC)
	bk.now = func() time.Time { return clock }

	openPeriodTestPosition(bk, "order-period")
	bk.Mark(map[string]float64{"AAPL": 110}) // NAV 101k

	snap := bk.Snapshot()
	if snap.MonthlyPnL != 1000 {
		t.Fatalf("expected +1000 monthly P&L in May, got %.2f", snap.MonthlyPnL)
	}

	clock = time.Date(2026, time.June, 2, 10, 0, 0, 0, time.UTC)
	bk.Mark(map[string]float64{"AAPL": 110}) // price unchanged across month roll

	snap = bk.Snapshot()
	if snap.MonthlyPnL != 0 {
		t.Fatalf("expected monthly P&L to reset at month boundary, got %.2f", snap.MonthlyPnL)
	}
	if snap.TotalPnL != 1000 {
		t.Fatalf("expected total P&L untouched by rollover, got %.2f", snap.TotalPnL)
	}

	clock = time.Date(2026, time.June, 3, 10, 0, 0, 0, time.UTC)
	bk.Mark(map[string]float64{"AAPL": 95}) // NAV 99.5k

	snap = bk.Snapshot()
	if snap.MonthlyPnL != -1500 {
		t.Fatalf("expected -1500 monthly P&L measured from June anchor, got %.2f", snap.MonthlyPnL)
	}
	if snap.WeeklyPnL != -1500 {
		t.Fatalf("expected -1500 weekly P&L measured from week anchor, got %.2f", snap.WeeklyPnL)
	}
	if snap.TotalPnL != -500 {
		t.Fatalf("expected -500 total P&L, got %.2f", snap.TotalPnL)
	}
}

func TestDailyRealizedPnLResetsOnNewDay(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	bk.SetDeskCapital("desk-1", 100000)
	clock := time.Date(2026, time.May, 20, 10, 0, 0, 0, time.UTC)
	bk.now = func() time.Time { return clock }

	openPeriodTestPosition(bk, "order-day")
	bk.Mark(map[string]float64{"AAPL": 110})
	if _, err := bk.ClosePosition("order-day", 110, "target_hit"); err != nil {
		t.Fatal(err)
	}

	snap := bk.Snapshot()
	if snap.DailyPnL != 1000 {
		t.Fatalf("expected +1000 realized daily P&L, got %.2f", snap.DailyPnL)
	}
	if snap.DeskPnL["desk-1"] != 1000 {
		t.Fatalf("expected +1000 desk daily P&L, got %.2f", snap.DeskPnL["desk-1"])
	}

	clock = time.Date(2026, time.May, 21, 10, 0, 0, 0, time.UTC)
	bk.Mark(map[string]float64{})

	snap = bk.Snapshot()
	if snap.DailyPnL != 0 {
		t.Fatalf("expected daily P&L reset on new day, got %.2f", snap.DailyPnL)
	}
	if snap.DeskPnL["desk-1"] != 0 {
		t.Fatalf("expected desk daily P&L reset on new day, got %.2f", snap.DeskPnL["desk-1"])
	}
}

func TestSnapshotRollsPeriodsOnQuietBook(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	bk.SetDeskCapital("desk-1", 100000)
	clock := time.Date(2026, time.May, 20, 10, 0, 0, 0, time.UTC)
	bk.now = func() time.Time { return clock }

	openPeriodTestPosition(bk, "order-quiet")
	bk.Mark(map[string]float64{"AAPL": 110})
	if _, err := bk.ClosePosition("order-quiet", 110, "target_hit"); err != nil {
		t.Fatal(err)
	}

	// No marks, fills, or reconciles cross the boundary — Snapshot alone
	// must still observe the new month and report fresh period figures.
	clock = time.Date(2026, time.June, 2, 10, 0, 0, 0, time.UTC)

	snap := bk.Snapshot()
	if snap.DailyPnL != 0 {
		t.Fatalf("expected daily P&L reset on quiet-book day roll, got %.2f", snap.DailyPnL)
	}
	if snap.MonthlyPnL != 0 {
		t.Fatalf("expected monthly P&L reset on quiet-book month roll, got %.2f", snap.MonthlyPnL)
	}
	if snap.TotalPnL != 1000 {
		t.Fatalf("expected total P&L preserved, got %.2f", snap.TotalPnL)
	}
}

func TestFillsRefreshMarkTimestamp(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	openClock := time.Date(2026, time.June, 10, 10, 0, 0, 0, time.UTC)
	clock := openClock
	bk.now = func() time.Time { return clock }

	openPeriodTestPosition(bk, "order-mark")
	pos, ok := bk.GetPosition("order-mark")
	if !ok {
		t.Fatal("expected position in book")
	}
	if !pos.MarkedAt.Equal(openClock) {
		t.Fatalf("expected MarkedAt set to fill time at open, got %v", pos.MarkedAt)
	}

	// A later fill that writes the price is a fresh observation and must
	// refresh the mark timestamp, or the monitor will cry stale on live data.
	pos.CurrentPrice = 0
	clock = openClock.Add(45 * time.Minute)
	inst := model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"}
	bk.ApplyExecutionFill(&model.Fill{
		OrderID:    "order-mark",
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   100,
		AvgPrice:   105,
		FilledAt:   clock,
	}, &model.Thesis{ID: "thesis-order-mark", DeskID: "desk-1", Instrument: inst, Direction: model.Long})

	pos, _ = bk.GetPosition("order-mark")
	if !pos.MarkedAt.Equal(clock) {
		t.Fatalf("expected fill to refresh MarkedAt to %v, got %v", clock, pos.MarkedAt)
	}
}

func TestDailyRolloverPinnedToNewYorkCalendar(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	bk.SetDeskCapital("desk-1", 100000)
	// 23:00 UTC on May 20 = 19:00 ET May 20
	clock := time.Date(2026, time.May, 20, 23, 0, 0, 0, time.UTC)
	bk.now = func() time.Time { return clock }

	openPeriodTestPosition(bk, "order-tz")
	bk.Mark(map[string]float64{"AAPL": 110})
	if _, err := bk.ClosePosition("order-tz", 110, "target_hit"); err != nil {
		t.Fatal(err)
	}

	// 03:00 UTC May 21 is still 23:00 ET May 20 — the trading day has not rolled
	clock = time.Date(2026, time.May, 21, 3, 0, 0, 0, time.UTC)
	snap := bk.Snapshot()
	if snap.DailyPnL != 1000 {
		t.Fatalf("expected daily P&L to survive UTC midnight (still May 20 in New York), got %.2f", snap.DailyPnL)
	}

	// 08:30 UTC May 21 is 04:30 ET May 21 — now the trading day has rolled
	clock = time.Date(2026, time.May, 21, 8, 30, 0, 0, time.UTC)
	snap = bk.Snapshot()
	if snap.DailyPnL != 0 {
		t.Fatalf("expected daily P&L reset on New York day roll, got %.2f", snap.DailyPnL)
	}
}
