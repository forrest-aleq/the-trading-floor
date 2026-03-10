package risk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
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
	positionPct := (order.Notional / deskCapital) * 100
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
	newGross := portfolio.GrossExposure + order.Notional
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
	cashDeployPct := ((portfolio.NAV - portfolio.Cash + order.Notional) / portfolio.NAV) * 100
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
			"symbol", order.Instrument.Symbol,
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
		"symbol", order.Instrument.Symbol,
		"notional", order.Notional,
	)

	adjustedOrder := order

	return model.RiskDecision{
		Allowed:       true,
		AdjustedOrder: &adjustedOrder,
		Token:         token,
	}
}

func (g *Gate) mintToken(order model.Order) *model.CapToken {
	nonce := uuid.New().String()
	data := fmt.Sprintf("%s:%s:%s:%.2f:%s",
		order.DeskID, order.Instrument.Symbol, order.Direction, order.Quantity, nonce)

	mac := hmac.New(sha256.New, g.secret)
	mac.Write([]byte(data))
	sig := hex.EncodeToString(mac.Sum(nil))

	return &model.CapToken{
		Capability: string(order.OrderType),
		Constraints: map[string]interface{}{
			"symbol":       order.Instrument.Symbol,
			"max_qty":      order.Quantity,
			"max_notional": order.Notional,
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
