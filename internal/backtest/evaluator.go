package backtest

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

type HistoricalSource interface {
	HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]ibkr.HistoricalBar, error)
}

type HistoricalPlan struct {
	EntryTime  time.Time
	EndTime    time.Time
	Duration   string
	BarSize    string
	WhatToShow string
	UseRTH     bool
}

func BuildHistoricalPlan(entryTime time.Time, thesis *model.Thesis) HistoricalPlan {
	horizon := thesis.TimeHorizon
	if horizon <= 0 {
		horizon = 24 * time.Hour
	}
	end := entryTime.Add(horizon)
	return HistoricalPlan{
		EntryTime:  entryTime,
		EndTime:    end,
		Duration:   historicalDuration(horizon),
		BarSize:    historicalBarSize(horizon),
		WhatToShow: historicalWhatToShow(thesis.PrimaryInstrument()),
		UseRTH:     useRegularTradingHours(thesis.PrimaryInstrument()),
	}
}

func NormalizePositionSize(thesis *model.Thesis, capital float64) {
	if thesis == nil || thesis.PositionSize <= 0 || thesis.EntryPrice <= 0 || capital <= 0 {
		return
	}

	targetNotional := capital * thesis.PositionSize
	unitNotional := thesis.GrossEntryNotional(1)
	if targetNotional <= 0 || unitNotional <= 0 {
		return
	}

	quantity := targetNotional / unitNotional
	switch thesis.PrimaryInstrument().SecType {
	case "OPT", "FUT", "FOP":
		quantity = math.Max(1, math.Floor(quantity))
	}
	thesis.PositionSize = quantity
}

func EvaluateHistoricalOutcome(thesis *model.Thesis, entryTime time.Time, bars []ibkr.HistoricalBar) (*model.ThesisOutcome, error) {
	if thesis == nil {
		return nil, fmt.Errorf("nil thesis")
	}
	if thesis.IsMultiLeg() {
		return &model.ThesisOutcome{
			Profitable:   false,
			ExitReason:   "backtest_unsupported_multi_leg",
			ErrorClass:   "policy_block",
			HoldingHours: 0,
		}, nil
	}
	if len(bars) == 0 {
		return &model.ThesisOutcome{
			Profitable:   false,
			ExitReason:   "backtest_no_bars",
			ErrorClass:   "infrastructure_error",
			HoldingHours: 0,
		}, nil
	}

	entryPrice := thesis.EntryPrice
	if entryPrice <= 0 {
		entryPrice = bars[0].Open
	}
	exitPrice := bars[len(bars)-1].Close
	exitTime := bars[len(bars)-1].Time
	exitReason := "time_horizon_elapsed"

	for _, bar := range bars {
		if bar.Time.Before(entryTime) {
			continue
		}

		stopHit, targetHit := stopAndTargetHit(thesis, bar)
		switch {
		case stopHit && targetHit:
			exitPrice = conservativeConflictExit(thesis)
			exitReason = "target_stop_same_bar"
			exitTime = bar.Time
			goto done
		case stopHit:
			exitPrice = thesis.StopLoss
			exitReason = "stop_loss_hit"
			exitTime = bar.Time
			goto done
		case targetHit:
			exitPrice = thesis.TargetPrice
			exitReason = "target_hit"
			exitTime = bar.Time
			goto done
		}
	}

done:
	quantity := thesis.PositionSize
	if quantity <= 0 {
		quantity = 1
	}
	multiplier := thesis.PrimaryInstrument().MultiplierValue()
	pnl := realizedPnL(thesis.Direction, entryPrice, exitPrice, quantity, multiplier)
	entryNotional := math.Abs(entryPrice * quantity * multiplier)
	returnPct := 0.0
	if entryNotional > 0 {
		returnPct = pnl / entryNotional * 100
	}
	expectedRisk := math.Abs(entryPrice-thesis.StopLoss) * quantity * multiplier
	riskReward := 0.0
	if expectedRisk > 0 {
		riskReward = pnl / expectedRisk
	}

	return &model.ThesisOutcome{
		Profitable:   pnl > 0,
		RealizedPnL:  pnl,
		ReturnPct:    returnPct,
		RiskReward:   riskReward,
		HoldingHours: exitTime.Sub(entryTime).Hours(),
		ExitReason:   exitReason,
	}, nil
}

func stopAndTargetHit(thesis *model.Thesis, bar ibkr.HistoricalBar) (bool, bool) {
	switch thesis.Direction {
	case model.Short:
		stopHit := thesis.StopLoss > 0 && bar.High >= thesis.StopLoss
		targetHit := thesis.TargetPrice > 0 && bar.Low <= thesis.TargetPrice
		return stopHit, targetHit
	default:
		stopHit := thesis.StopLoss > 0 && bar.Low <= thesis.StopLoss
		targetHit := thesis.TargetPrice > 0 && bar.High >= thesis.TargetPrice
		return stopHit, targetHit
	}
}

func conservativeConflictExit(thesis *model.Thesis) float64 {
	if thesis.Direction == model.Short {
		if thesis.StopLoss > 0 {
			return thesis.StopLoss
		}
		return thesis.TargetPrice
	}
	if thesis.StopLoss > 0 {
		return thesis.StopLoss
	}
	return thesis.TargetPrice
}

func realizedPnL(direction model.TradeDirection, entryPrice, exitPrice, quantity, multiplier float64) float64 {
	switch direction {
	case model.Short:
		return (entryPrice - exitPrice) * quantity * multiplier
	default:
		return (exitPrice - entryPrice) * quantity * multiplier
	}
}

func historicalDuration(horizon time.Duration) string {
	if horizon <= 0 {
		return "1 D"
	}
	days := int(math.Ceil(horizon.Hours()/24.0)) + 1
	if days < 1 {
		days = 1
	}
	return fmt.Sprintf("%d D", days)
}

func historicalBarSize(horizon time.Duration) string {
	switch {
	case horizon <= 6*time.Hour:
		return "5 mins"
	case horizon <= 24*time.Hour:
		return "15 mins"
	case horizon <= 72*time.Hour:
		return "1 hour"
	case horizon <= 14*24*time.Hour:
		return "4 hours"
	default:
		return "1 day"
	}
}

func historicalWhatToShow(inst model.Instrument) string {
	switch strings.ToUpper(strings.TrimSpace(inst.SecType)) {
	case "CASH", "CFD":
		return "MIDPOINT"
	default:
		return "TRADES"
	}
}

func useRegularTradingHours(inst model.Instrument) bool {
	switch strings.ToUpper(strings.TrimSpace(inst.SecType)) {
	case "STK", "OPT":
		return true
	default:
		return false
	}
}
