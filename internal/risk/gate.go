package risk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/pkg/model"
)

// Gate is the deterministic pre-trade risk check. No LLM. No exceptions.
type Gate struct {
	log    *slog.Logger
	limits Limits
	secret []byte // For capability token signing
}

// Limits are the hard-coded risk parameters from policies.json
type Limits struct {
	// Per-desk
	MaxDailyLossPct        float64
	MaxSinglePositionPct   float64
	MaxCorrelatedPositions int
	MaxOpenPositions       int
	CapitalPerDesk         float64

	// Per-trade
	MaxPositionSizePct float64
	MinConvictionScore float64

	// Portfolio-level
	TotalCapital          float64
	MaxFactorExposurePct  float64
	MaxDrawdownPct        float64
	KillSwitchDrawdownPct float64
	MaxGrossExposurePct   float64
	MaxNetExposurePct     float64
	MaxCashDeployPct      float64
}

func DefaultLimits() Limits {
	return Limits{
		MaxDailyLossPct:        3.0,
		MaxSinglePositionPct:   20.0,
		MaxCorrelatedPositions: 3,
		MaxOpenPositions:       10,
		CapitalPerDesk:         25000,
		MaxPositionSizePct:     10.0,
		MinConvictionScore:     0.65,
		TotalCapital:           1000000,
		MaxFactorExposurePct:   25.0,
		MaxDrawdownPct:         10.0,
		KillSwitchDrawdownPct:  15.0,
		MaxGrossExposurePct:    200.0,
		MaxNetExposurePct:      100.0,
		MaxCashDeployPct:       80.0,
	}
}

func NewGate(limits Limits) *Gate {
	return &Gate{
		log:    slog.Default().With("component", "risk"),
		limits: limits,
		secret: []byte("trading-floor-token-secret"), // TODO: from env
	}
}

// PortfolioState is the current state needed for risk checks
type PortfolioState struct {
	NAV           float64
	Cash          float64
	GrossExposure float64
	NetExposure   float64
	DailyPnL      float64
	WeeklyPnL     float64
	MonthlyPnL    float64
	OpenPositions int
	DeskPositions map[string]int     // desk_id → open position count
	DeskDailyPnL  map[string]float64 // desk_id → daily P&L
	DeskCapital   map[string]float64 // desk_id → allocated capital
}

// Check validates an order against all risk limits
func (g *Gate) Check(order model.Order, thesis *model.Thesis, portfolio PortfolioState) model.RiskDecision {
	violations := []model.Violation{}

	deskCapital := portfolio.DeskCapital[order.DeskID]
	if deskCapital == 0 {
		deskCapital = g.limits.CapitalPerDesk
	}

	// 1. Conviction threshold
	if thesis != nil && thesis.Conviction < g.limits.MinConvictionScore {
		violations = append(violations, model.Violation{
			Rule:    "min_conviction",
			Limit:   fmt.Sprintf("%.2f", g.limits.MinConvictionScore),
			Current: fmt.Sprintf("%.2f", thesis.Conviction),
		})
	}

	// 2. Position size vs desk capital
	orderNotional := order.GrossNotional()
	riskExposure := orderNotional
	if order.IsMultiLeg() {
		maxLoss, err := definedRiskExposure(order)
		if err != nil {
			violations = append(violations, model.Violation{
				Rule:    "unsupported_multi_leg_structure",
				Limit:   "defined_risk_vertical_only",
				Current: err.Error(),
			})
		} else {
			riskExposure = maxLoss
		}
	}
	positionPct := (riskExposure / deskCapital) * 100
	if positionPct > g.limits.MaxSinglePositionPct {
		violations = append(violations, model.Violation{
			Rule:    "max_single_position_pct",
			Limit:   fmt.Sprintf("%.1f%%", g.limits.MaxSinglePositionPct),
			Current: fmt.Sprintf("%.1f%%", positionPct),
		})
	}

	// 3. Max open positions per desk
	deskPos := portfolio.DeskPositions[order.DeskID]
	if deskPos >= g.limits.MaxOpenPositions {
		violations = append(violations, model.Violation{
			Rule:    "max_open_positions",
			Limit:   fmt.Sprintf("%d", g.limits.MaxOpenPositions),
			Current: fmt.Sprintf("%d", deskPos),
		})
	}

	// 4. Daily loss limit per desk
	deskPnL := portfolio.DeskDailyPnL[order.DeskID]
	deskLossPct := (-deskPnL / deskCapital) * 100
	if deskLossPct >= g.limits.MaxDailyLossPct {
		violations = append(violations, model.Violation{
			Rule:    "desk_daily_loss_halt",
			Limit:   fmt.Sprintf("%.1f%%", g.limits.MaxDailyLossPct),
			Current: fmt.Sprintf("%.1f%%", deskLossPct),
		})
	}

	// 5. Portfolio gross exposure
	newGross := portfolio.GrossExposure + riskExposure
	grossPct := (newGross / portfolio.NAV) * 100
	if grossPct > g.limits.MaxGrossExposurePct {
		violations = append(violations, model.Violation{
			Rule:    "max_gross_exposure",
			Limit:   fmt.Sprintf("%.0f%%", g.limits.MaxGrossExposurePct),
			Current: fmt.Sprintf("%.0f%%", grossPct),
		})
	}

	// 6. Kill switch check — portfolio-level drawdown
	monthlyLossPct := (-portfolio.MonthlyPnL / g.limits.TotalCapital) * 100
	if monthlyLossPct >= g.limits.KillSwitchDrawdownPct {
		violations = append(violations, model.Violation{
			Rule:    "KILL_SWITCH",
			Limit:   fmt.Sprintf("%.0f%%", g.limits.KillSwitchDrawdownPct),
			Current: fmt.Sprintf("%.1f%%", monthlyLossPct),
		})
	}

	// 7. Cash deployment limit
	cashDeployPct := ((portfolio.NAV - portfolio.Cash + riskExposure) / portfolio.NAV) * 100
	if cashDeployPct > g.limits.MaxCashDeployPct {
		violations = append(violations, model.Violation{
			Rule:    "max_cash_deploy",
			Limit:   fmt.Sprintf("%.0f%%", g.limits.MaxCashDeployPct),
			Current: fmt.Sprintf("%.0f%%", cashDeployPct),
		})
	}

	if len(violations) > 0 {
		g.log.Warn("order rejected by risk gate",
			"desk_id", order.DeskID,
			"symbol", order.DisplaySymbol(),
			"violations", len(violations),
		)
		return model.RiskDecision{
			Allowed:    false,
			Violations: violations,
		}
	}

	// Mint capability token
	token := g.mintToken(order)

	g.log.Info("order approved by risk gate",
		"desk_id", order.DeskID,
		"symbol", order.DisplaySymbol(),
		"notional", orderNotional,
		"risk_exposure", riskExposure,
	)

	adjustedOrder := order

	return model.RiskDecision{
		Allowed:       true,
		AdjustedOrder: &adjustedOrder,
		Token:         token,
	}
}

func definedRiskExposure(order model.Order) (float64, error) {
	structure := strings.ToLower(strings.TrimSpace(order.Structure))
	switch structure {
	case "bull_call_spread":
		return verticalSpreadMaxLoss(order, "C", model.Long, model.Short, true)
	case "bear_put_spread":
		return verticalSpreadMaxLoss(order, "P", model.Long, model.Short, false)
	default:
		return 0, fmt.Errorf("structure %q not enabled", structure)
	}
}

func verticalSpreadMaxLoss(order model.Order, right string, lowerStrikeDirection, higherStrikeDirection model.TradeDirection, lowerStrikeFirst bool) (float64, error) {
	if len(order.Legs) != 2 {
		return 0, fmt.Errorf("expected 2 legs, got %d", len(order.Legs))
	}
	if order.Direction != model.Long {
		return 0, fmt.Errorf("debit verticals must use long combo direction")
	}

	legs := append([]model.TradeLeg(nil), order.Legs...)
	if legs[0].Instrument.Strike > legs[1].Instrument.Strike {
		legs[0], legs[1] = legs[1], legs[0]
	}
	lower := legs[0]
	higher := legs[1]

	if err := validateVerticalLegPair(lower, higher, right); err != nil {
		return 0, err
	}
	if lowerStrikeFirst {
		if lower.Direction != lowerStrikeDirection || higher.Direction != higherStrikeDirection {
			return 0, fmt.Errorf("leg directions do not match %s structure", strings.ToLower(right))
		}
	} else {
		if higher.Direction != lowerStrikeDirection || lower.Direction != higherStrikeDirection {
			return 0, fmt.Errorf("leg directions do not match %s structure", strings.ToLower(right))
		}
	}

	width := higher.Instrument.Strike - lower.Instrument.Strike
	if width <= 0 {
		return 0, fmt.Errorf("spread width must be positive")
	}

	entry := order.LimitPrice
	if entry <= 0 {
		return 0, fmt.Errorf("debit vertical requires positive entry price")
	}
	if entry >= width {
		return 0, fmt.Errorf("entry price %.2f must be below spread width %.2f", entry, width)
	}
	units := order.Quantity
	if units <= 0 {
		units = 1
	}
	multiplier := lower.Instrument.MultiplierValue()
	return entry * units * multiplier, nil
}

func validateVerticalLegPair(lower, higher model.TradeLeg, right string) error {
	lowerInst := lower.Instrument
	higherInst := higher.Instrument
	if lowerInst.Symbol == "" || higherInst.Symbol == "" {
		return fmt.Errorf("legs require symbols")
	}
	if lowerInst.Symbol != higherInst.Symbol {
		return fmt.Errorf("vertical spread must share underlying")
	}
	if lowerInst.SecType != higherInst.SecType {
		return fmt.Errorf("vertical spread legs must share sec_type")
	}
	if lowerInst.SecType != "OPT" && lowerInst.SecType != "FOP" {
		return fmt.Errorf("vertical spread requires option legs")
	}
	if lowerInst.Expiry == "" || lowerInst.Expiry != higherInst.Expiry {
		return fmt.Errorf("vertical spread requires same expiry")
	}
	if !strings.EqualFold(lowerInst.Right, right) || !strings.EqualFold(higherInst.Right, right) {
		return fmt.Errorf("vertical spread requires %s legs", right)
	}
	if lower.EffectiveRatio() != higher.EffectiveRatio() {
		return fmt.Errorf("vertical spread requires 1:1 ratio")
	}
	if lowerInst.Strike <= 0 || higherInst.Strike <= 0 {
		return fmt.Errorf("vertical spread requires strikes")
	}
	return nil
}

func (g *Gate) mintToken(order model.Order) *model.CapToken {
	orderNotional := order.GrossNotional()
	nonce := uuid.New().String()
	data := fmt.Sprintf("%s:%s:%s:%.2f:%s",
		order.DeskID, order.DisplaySymbol(), order.Direction, order.Quantity, nonce)

	mac := hmac.New(sha256.New, g.secret)
	mac.Write([]byte(data))
	sig := hex.EncodeToString(mac.Sum(nil))

	return &model.CapToken{
		Capability: order.ExecutionCapability(),
		Constraints: map[string]interface{}{
			"symbol":       order.DisplaySymbol(),
			"max_qty":      order.Quantity,
			"max_notional": orderNotional,
			"structure":    order.Structure,
		},
		DeskID:    order.DeskID,
		Expiry:    time.Now().Add(60 * time.Minute),
		Nonce:     nonce,
		Signature: sig,
	}
}

// IsKillSwitch returns true if the kill switch violation was triggered
func IsKillSwitch(decision model.RiskDecision) bool {
	for _, v := range decision.Violations {
		if v.Rule == "KILL_SWITCH" {
			return true
		}
	}
	return false
}
