package quant

import (
	"fmt"
	"math"
	"strings"

	"github.com/hnic/trading-floor/pkg/model"
)

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) AnalyzeThesis(thesis *model.Thesis) *model.QuantMetrics {
	if thesis == nil {
		return nil
	}

	switch strings.ToLower(strings.TrimSpace(thesis.Structure)) {
	case "", "single":
		return analyzeSingle(thesis)
	case "bull_call_spread":
		return analyzeBullCallSpread(thesis)
	case "bear_put_spread":
		return analyzeBearPutSpread(thesis)
	default:
		return &model.QuantMetrics{
			Method:      "unsupported_structure",
			DefinedRisk: false,
			Warnings:    []string{"quant toolbox does not support this structure yet"},
		}
	}
}

func analyzeSingle(thesis *model.Thesis) *model.QuantMetrics {
	inst := thesis.PrimaryInstrument()
	mult := inst.MultiplierValue()
	qty := math.Max(thesis.PositionSize, 1)
	entry := thesis.EntryPrice
	if entry <= 0 {
		entry = currentUnderlyingPrice(thesis)
	}

	metrics := &model.QuantMetrics{
		Method:       "deterministic_single",
		DefinedRisk:  false,
		Breakeven:    entry,
		NetDeltaBias: singleDeltaBias(thesis.Direction, inst),
	}

	switch inst.SecType {
	case "OPT", "FOP":
		if thesis.Direction == model.Long {
			metrics.DefinedRisk = true
			metrics.MaxLoss = entry * qty * mult
			metrics.MarginEstimate = metrics.MaxLoss
		} else {
			metrics.MarginEstimate = entry * qty * mult * 2
			metrics.Warnings = append(metrics.Warnings, "short option has undefined tail risk without margin-side protections")
		}
		metrics.Breakeven = optionBreakeven(inst, thesis.Direction, entry)
	default:
		notional := inst.Notional(entry, qty)
		if thesis.Direction == model.Short {
			metrics.MarginEstimate = notional * 1.5
			metrics.Warnings = append(metrics.Warnings, "short cash equity risk is not strictly bounded")
		} else {
			metrics.MarginEstimate = notional
		}
		if stopRisk := stopLossRisk(thesis, qty, mult); stopRisk > 0 {
			metrics.MaxLoss = stopRisk
		} else if thesis.Direction == model.Long && entry > 0 {
			metrics.MaxLoss = entry * qty * mult
		}
	}

	if targetGain := targetGain(thesis, qty, mult); targetGain > 0 {
		metrics.MaxGain = targetGain
	}
	if metrics.MaxLoss > 0 && metrics.MaxGain > 0 {
		metrics.RewardToRisk = metrics.MaxGain / metrics.MaxLoss
	}
	metrics.Scenarios = singleScenarios(thesis, entry, qty, mult)
	return metrics
}

func analyzeBullCallSpread(thesis *model.Thesis) *model.QuantMetrics {
	return analyzeVerticalSpread(thesis, "bull_call_spread")
}

func analyzeBearPutSpread(thesis *model.Thesis) *model.QuantMetrics {
	return analyzeVerticalSpread(thesis, "bear_put_spread")
}

func analyzeVerticalSpread(thesis *model.Thesis, structure string) *model.QuantMetrics {
	if thesis == nil || len(thesis.Legs) != 2 {
		return &model.QuantMetrics{
			Method:      "vertical_invalid",
			DefinedRisk: false,
			Warnings:    []string{"vertical spread requires exactly two legs"},
		}
	}

	longLeg, shortLeg := splitLegs(thesis.Legs)
	if longLeg == nil || shortLeg == nil {
		return &model.QuantMetrics{
			Method:      "vertical_invalid",
			DefinedRisk: false,
			Warnings:    []string{"vertical spread requires one long leg and one short leg"},
		}
	}

	width := math.Abs(longLeg.Instrument.Strike - shortLeg.Instrument.Strike)
	if width <= 0 {
		return &model.QuantMetrics{
			Method:      "vertical_invalid",
			DefinedRisk: false,
			Warnings:    []string{"vertical spread requires distinct strikes"},
		}
	}

	qty := math.Max(thesis.PositionSize, 1)
	mult := longLeg.Instrument.MultiplierValue()
	debit := thesis.EntryPrice
	if debit <= 0 {
		debit = netDebitFromLegs(thesis.Legs)
	}

	metrics := &model.QuantMetrics{
		Method:         "expiry_payoff_vertical",
		DefinedRisk:    true,
		MaxLoss:        debit * qty * mult,
		MaxGain:        math.Max(width-debit, 0) * qty * mult,
		MarginEstimate: debit * qty * mult,
		NetDeltaBias:   0.60,
	}
	if structure == "bear_put_spread" {
		metrics.NetDeltaBias = -0.60
	}
	if metrics.MaxLoss > 0 && metrics.MaxGain > 0 {
		metrics.RewardToRisk = metrics.MaxGain / metrics.MaxLoss
	}

	switch structure {
	case "bull_call_spread":
		metrics.Breakeven = longLeg.Instrument.Strike + debit
	case "bear_put_spread":
		metrics.Breakeven = longLeg.Instrument.Strike - debit
	}

	spot := currentUnderlyingPrice(thesis)
	if spot <= 0 {
		spot = longLeg.Instrument.Strike
		metrics.Warnings = append(metrics.Warnings, "underlying spot unavailable; scenario analysis anchored to strike")
	}
	metrics.Scenarios = verticalScenarios(structure, *longLeg, *shortLeg, debit, qty, mult, spot)
	return metrics
}

func splitLegs(legs []model.TradeLeg) (*model.TradeLeg, *model.TradeLeg) {
	var longLeg *model.TradeLeg
	var shortLeg *model.TradeLeg
	for i := range legs {
		leg := legs[i]
		if leg.Direction == model.Long && longLeg == nil {
			longLeg = &leg
			continue
		}
		if leg.Direction == model.Short && shortLeg == nil {
			shortLeg = &leg
		}
	}
	return longLeg, shortLeg
}

func singleScenarios(thesis *model.Thesis, entry, qty, mult float64) []model.QuantScenario {
	if entry <= 0 {
		return nil
	}
	moves := []float64{-0.10, -0.05, 0, 0.05, 0.10}
	scenarios := make([]model.QuantScenario, 0, len(moves))
	inst := thesis.PrimaryInstrument()
	for _, move := range moves {
		underlying := entry * (1 + move)
		pnl := 0.0
		switch inst.SecType {
		case "OPT", "FOP":
			pnl = optionPayoff(inst, thesis.Direction, entry, underlying, qty, mult)
		default:
			if thesis.Direction == model.Long {
				pnl = (underlying - entry) * qty * mult
			} else {
				pnl = (entry - underlying) * qty * mult
			}
		}
		scenarios = append(scenarios, model.QuantScenario{
			Label:             fmt.Sprintf("%+.0f%%", move*100),
			UnderlyingMovePct: move * 100,
			UnderlyingPrice:   underlying,
			EstimatedPnL:      pnl,
		})
	}
	return scenarios
}

func verticalScenarios(structure string, longLeg, shortLeg model.TradeLeg, debit, qty, mult, spot float64) []model.QuantScenario {
	moves := []float64{-0.10, -0.05, 0, 0.05, 0.10}
	scenarios := make([]model.QuantScenario, 0, len(moves))
	for _, move := range moves {
		underlying := spot * (1 + move)
		longIntrinsic := optionIntrinsic(longLeg.Instrument, underlying)
		shortIntrinsic := optionIntrinsic(shortLeg.Instrument, underlying)
		pnl := ((longIntrinsic - shortIntrinsic) - debit) * qty * mult
		scenarios = append(scenarios, model.QuantScenario{
			Label:             fmt.Sprintf("%s %+.0f%%", structure, move*100),
			UnderlyingMovePct: move * 100,
			UnderlyingPrice:   underlying,
			EstimatedPnL:      pnl,
		})
	}
	return scenarios
}

func stopLossRisk(thesis *model.Thesis, qty, mult float64) float64 {
	if thesis.EntryPrice <= 0 || thesis.StopLoss <= 0 {
		return 0
	}
	switch thesis.Direction {
	case model.Long:
		if thesis.StopLoss >= thesis.EntryPrice {
			return 0
		}
		return (thesis.EntryPrice - thesis.StopLoss) * qty * mult
	case model.Short:
		if thesis.StopLoss <= thesis.EntryPrice {
			return 0
		}
		return (thesis.StopLoss - thesis.EntryPrice) * qty * mult
	default:
		return 0
	}
}

func targetGain(thesis *model.Thesis, qty, mult float64) float64 {
	if thesis.EntryPrice <= 0 || thesis.TargetPrice <= 0 {
		return 0
	}
	switch thesis.Direction {
	case model.Long:
		if thesis.TargetPrice <= thesis.EntryPrice {
			return 0
		}
		return (thesis.TargetPrice - thesis.EntryPrice) * qty * mult
	case model.Short:
		if thesis.TargetPrice >= thesis.EntryPrice {
			return 0
		}
		return (thesis.EntryPrice - thesis.TargetPrice) * qty * mult
	default:
		return 0
	}
}

func currentUnderlyingPrice(thesis *model.Thesis) float64 {
	if thesis == nil {
		return 0
	}
	if thesis.MarketContext != nil && thesis.MarketContext.CurrentPrice > 0 {
		return thesis.MarketContext.CurrentPrice
	}
	if thesis.EntryPrice > 0 && thesis.PrimaryInstrument().SecType != "OPT" && thesis.PrimaryInstrument().SecType != "FOP" {
		return thesis.EntryPrice
	}
	return 0
}

func singleDeltaBias(direction model.TradeDirection, inst model.Instrument) float64 {
	bias := 1.0
	if inst.SecType == "OPT" || inst.SecType == "FOP" {
		bias = 0.5
	}
	if direction == model.Short {
		return -bias
	}
	return bias
}

func optionBreakeven(inst model.Instrument, direction model.TradeDirection, premium float64) float64 {
	if direction == model.Short {
		return 0
	}
	switch strings.ToUpper(strings.TrimSpace(inst.Right)) {
	case "C":
		return inst.Strike + premium
	case "P":
		return inst.Strike - premium
	default:
		return premium
	}
}

func optionPayoff(inst model.Instrument, direction model.TradeDirection, premium, underlying, qty, mult float64) float64 {
	intrinsic := optionIntrinsic(inst, underlying)
	if direction == model.Long {
		return (intrinsic - premium) * qty * mult
	}
	return (premium - intrinsic) * qty * mult
}

func optionIntrinsic(inst model.Instrument, underlying float64) float64 {
	switch strings.ToUpper(strings.TrimSpace(inst.Right)) {
	case "C":
		return math.Max(underlying-inst.Strike, 0)
	case "P":
		return math.Max(inst.Strike-underlying, 0)
	default:
		return 0
	}
}

func netDebitFromLegs(legs []model.TradeLeg) float64 {
	total := 0.0
	for _, leg := range legs {
		price := leg.EntryPrice
		if price <= 0 {
			continue
		}
		if leg.Direction == model.Long {
			total += price * leg.EffectiveRatio()
		} else {
			total -= price * leg.EffectiveRatio()
		}
	}
	if total < 0 {
		return 0
	}
	return total
}

func FormatForPrompt(metrics *model.QuantMetrics) string {
	if metrics == nil {
		return ""
	}
	lines := []string{
		"Quant toolbox:",
		fmt.Sprintf("  Method: %s", metrics.Method),
		fmt.Sprintf("  Defined risk: %t", metrics.DefinedRisk),
	}
	if metrics.MaxLoss > 0 {
		lines = append(lines, fmt.Sprintf("  Max loss: %.2f", metrics.MaxLoss))
	}
	if metrics.MaxGain > 0 {
		lines = append(lines, fmt.Sprintf("  Max gain: %.2f", metrics.MaxGain))
	}
	if metrics.Breakeven != 0 {
		lines = append(lines, fmt.Sprintf("  Breakeven: %.2f", metrics.Breakeven))
	}
	if metrics.MarginEstimate > 0 {
		lines = append(lines, fmt.Sprintf("  Margin estimate: %.2f", metrics.MarginEstimate))
	}
	if metrics.RewardToRisk > 0 {
		lines = append(lines, fmt.Sprintf("  Reward/risk: %.2f", metrics.RewardToRisk))
	}
	if metrics.NetDeltaBias != 0 {
		lines = append(lines, fmt.Sprintf("  Net delta bias: %.2f", metrics.NetDeltaBias))
	}
	for _, scenario := range metrics.Scenarios {
		lines = append(lines, fmt.Sprintf("  Scenario %s: underlying %.2f -> pnl %.2f", scenario.Label, scenario.UnderlyingPrice, scenario.EstimatedPnL))
	}
	if len(metrics.Warnings) > 0 {
		lines = append(lines, "  Warnings: "+strings.Join(metrics.Warnings, "; "))
	}
	return strings.Join(lines, "\n")
}
