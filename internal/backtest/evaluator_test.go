package backtest

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

func TestEvaluateHistoricalOutcomeHitsTargetForLong(t *testing.T) {
	thesis := &model.Thesis{
		Instrument:   model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		EntryPrice:   100,
		TargetPrice:  110,
		StopLoss:     95,
		PositionSize: 10,
	}
	entry := time.Date(2026, 4, 15, 9, 30, 0, 0, time.UTC)
	outcome, err := EvaluateHistoricalOutcome(thesis, entry, []ibkr.HistoricalBar{
		{Time: entry.Add(15 * time.Minute), Open: 101, High: 111, Low: 100, Close: 109},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ExitReason != "target_hit" {
		t.Fatalf("expected target exit, got %q", outcome.ExitReason)
	}
	if !outcome.Profitable || outcome.RealizedPnL <= 0 {
		t.Fatalf("expected profitable target outcome, got %+v", outcome)
	}
}

func TestEvaluateHistoricalOutcomeUsesConservativeSameBarConflict(t *testing.T) {
	thesis := &model.Thesis{
		Instrument:   model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		EntryPrice:   100,
		TargetPrice:  110,
		StopLoss:     95,
		PositionSize: 10,
	}
	entry := time.Date(2026, 4, 15, 9, 30, 0, 0, time.UTC)
	outcome, err := EvaluateHistoricalOutcome(thesis, entry, []ibkr.HistoricalBar{
		{Time: entry.Add(15 * time.Minute), Open: 101, High: 111, Low: 94, Close: 109},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ExitReason != "target_stop_same_bar" {
		t.Fatalf("expected conservative same-bar exit, got %q", outcome.ExitReason)
	}
	if outcome.Profitable {
		t.Fatalf("expected conservative stop to lose, got %+v", outcome)
	}
}

func TestEvaluateHistoricalOutcomeFallsBackToTimeHorizonExit(t *testing.T) {
	thesis := &model.Thesis{
		Instrument:   model.Instrument{Symbol: "TLT", SecType: "STK", Currency: "USD"},
		Direction:    model.Long,
		EntryPrice:   100,
		TargetPrice:  110,
		StopLoss:     95,
		PositionSize: 10,
	}
	entry := time.Date(2026, 4, 15, 9, 30, 0, 0, time.UTC)
	outcome, err := EvaluateHistoricalOutcome(thesis, entry, []ibkr.HistoricalBar{
		{Time: entry.Add(1 * time.Hour), Open: 100, High: 104, Low: 99, Close: 103},
		{Time: entry.Add(2 * time.Hour), Open: 103, High: 106, Low: 102, Close: 105},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ExitReason != "time_horizon_elapsed" {
		t.Fatalf("expected time horizon exit, got %q", outcome.ExitReason)
	}
	if !outcome.Profitable {
		t.Fatalf("expected positive close-based outcome, got %+v", outcome)
	}
}

func TestNormalizePositionSizeUsesCapitalNotional(t *testing.T) {
	thesis := &model.Thesis{
		Instrument:   model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"},
		EntryPrice:   100,
		PositionSize: 0.02,
	}
	NormalizePositionSize(thesis, 100000)
	if thesis.PositionSize != 20 {
		t.Fatalf("expected normalized quantity 20, got %.4f", thesis.PositionSize)
	}
}
