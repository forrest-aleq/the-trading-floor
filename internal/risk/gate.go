package risk

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
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
	MaxDailyLossPct        float64 `json:"max_daily_loss_pct"`
	MaxSinglePositionPct   float64 `json:"max_single_position_pct"`
	MaxCorrelatedPositions int     `json:"max_correlated_positions"`
	MaxOpenPositions       int     `json:"max_open_positions"`
	CapitalPerDesk         float64 `json:"capital_per_desk"`

	// Per-trade
	MaxPositionSizePct float64 `json:"max_position_size_pct"`
	MinConvictionScore float64 `json:"min_conviction_score"`
	MaxQuoteAgeSeconds float64 `json:"max_quote_age_seconds"`
	MaxEquitySpreadBps float64 `json:"max_equity_spread_bps"`
	MaxOptionSpreadBps float64 `json:"max_option_spread_bps"`

	// Portfolio-level
	TotalCapital          float64 `json:"total_capital"`
	MaxFactorExposurePct  float64 `json:"max_factor_exposure_pct"`
	MaxDrawdownPct        float64 `json:"max_drawdown_pct"`
	KillSwitchDrawdownPct float64 `json:"kill_switch_drawdown_pct"`
	MaxGrossExposurePct   float64 `json:"max_gross_exposure_pct"`
	MaxNetExposurePct     float64 `json:"max_net_exposure_pct"`
	MaxCashDeployPct      float64 `json:"max_cash_deploy_pct"`
}

func DefaultLimits() Limits {
	return loadActiveLimits()
}

func NewGate(limits Limits) *Gate {
	log := slog.Default().With("component", "risk")
	return &Gate{
		log:    log,
		limits: limits,
		secret: loadTokenSecret(log),
	}
}

func loadTokenSecret(log *slog.Logger) []byte {
	if secret := strings.TrimSpace(os.Getenv("RISK_TOKEN_SECRET")); secret != "" {
		if len(secret) < 32 {
			log.Error("RISK_TOKEN_SECRET is too short; refusing startup with invalid configured secret",
				"configured_length", len(secret),
				"minimum_length", 32,
			)
			panic("RISK_TOKEN_SECRET must be at least 32 characters")
		} else {
			return []byte(secret)
		}
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err == nil {
		log.Warn("RISK_TOKEN_SECRET not set; using ephemeral session secret")
		return []byte(hex.EncodeToString(buf))
	}

	log.Error("RISK_TOKEN_SECRET not set and random secret generation failed; refusing insecure startup")
	panic("crypto/rand unavailable: cannot generate secure token signing secret")
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
	if thesis != nil {
		if allowed, reason := thesis.EvidenceRiskGate(); !allowed {
			violations = append(violations, evidenceViolation(reason, thesis))
		}
	}
	if thesis != nil && thesis.MarketContext != nil {
		violations = append(violations, g.marketQualityViolations(order, thesis.MarketContext)...)
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
	} else if thesis != nil && thesis.QuantMetrics != nil && thesis.QuantMetrics.MarginEstimate > riskExposure {
		riskExposure = thesis.QuantMetrics.MarginEstimate
	}
	positionPct := (riskExposure / deskCapital) * 100
	if g.limits.MaxPositionSizePct > 0 && positionPct > g.limits.MaxPositionSizePct {
		violations = append(violations, model.Violation{
			Rule:    "max_position_size_pct",
			Limit:   fmt.Sprintf("%.1f%%", g.limits.MaxPositionSizePct),
			Current: fmt.Sprintf("%.1f%%", positionPct),
		})
	}
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

func (g *Gate) marketQualityViolations(order model.Order, marketCtx *model.MarketContext) []model.Violation {
	if g == nil || marketCtx == nil {
		return nil
	}

	violations := make([]model.Violation, 0, 2)
	if g.limits.MaxQuoteAgeSeconds > 0 && marketCtx.QuoteAgeSeconds > g.limits.MaxQuoteAgeSeconds {
		violations = append(violations, model.Violation{
			Rule:    "stale_quote",
			Limit:   fmt.Sprintf("%.0fs", g.limits.MaxQuoteAgeSeconds),
			Current: fmt.Sprintf("%.1fs", marketCtx.QuoteAgeSeconds),
		})
	}

	spreadLimit := g.quoteSpreadLimit(order.PrimaryInstrument())
	if spreadLimit > 0 && marketCtx.SpreadBps > spreadLimit {
		violations = append(violations, model.Violation{
			Rule:    "quote_spread_too_wide",
			Limit:   fmt.Sprintf("%.1f bps", spreadLimit),
			Current: fmt.Sprintf("%.1f bps", marketCtx.SpreadBps),
		})
	}
	return violations
}

func (g *Gate) quoteSpreadLimit(inst model.Instrument) float64 {
	switch strings.ToUpper(strings.TrimSpace(inst.SecType)) {
	case "OPT", "FOP":
		return g.limits.MaxOptionSpreadBps
	default:
		return g.limits.MaxEquitySpreadBps
	}
}

func evidenceViolation(reason string, thesis *model.Thesis) model.Violation {
	current := "unavailable"
	if thesis != nil && thesis.EvidenceMeta != nil {
		current = fmt.Sprintf("score=%.2f trust=%.2f freshness=%s contradictions=%d",
			thesis.EvidenceMeta.EvidenceScore,
			thesis.EvidenceMeta.SourceTrust,
			thesis.EvidenceMeta.FreshnessStatus,
			thesis.EvidenceMeta.ContradictionCount,
		)
		if vector := thesis.EvidenceMeta.ConfidenceVector; vector != nil && vector.Present() {
			current += fmt.Sprintf(" fact=%.2f novelty=%.2f market_map=%.2f expression=%.2f execution=%.2f competence=%.2f",
				vector.FactConfidence,
				vector.NoveltyConfidence,
				vector.MarketMappingConfidence,
				vector.ExpressionConfidence,
				vector.ExecutionConfidence,
				vector.CompetenceConfidence,
			)
		}
	}

	limit := "deterministic_evidence_gate"
	switch reason {
	case "stale_signal_evidence":
		limit = "fresh_evidence_required"
	case "contradictory_signal_evidence":
		limit = "no_high_severity_conflicts"
	case "uncorroborated_social_signal":
		limit = "independent_owner_group_corroboration"
	case "low_integrity_evidence":
		limit = "trust>=0.45_or_independent_corroboration"
	case "low_fact_confidence":
		limit = "fact_confidence>=0.30"
	case "low_market_mapping_confidence":
		limit = "market_mapping_confidence>=0.25"
	case "low_expression_confidence":
		limit = "expression_confidence>=0.22"
	case "low_execution_confidence":
		limit = "execution_confidence>=0.20"
	case "low_competence_confidence":
		limit = "competence_confidence>=0.20"
	case "low_evidence_score":
		limit = "evidence_score>=0.30"
	}

	return model.Violation{
		Rule:    reason,
		Limit:   limit,
		Current: current,
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
	expiry := time.Now().UTC().Add(60 * time.Minute)
	data := tokenSigningData(order, nonce, expiry)

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
		Expiry:    expiry,
		Nonce:     nonce,
		Signature: sig,
	}
}

// ValidateCapabilityToken verifies that a risk-issued token authorizes exactly
// the order being submitted.
func (g *Gate) ValidateCapabilityToken(token *model.CapToken, order model.Order) error {
	return g.validateCapabilityTokenAt(token, order, time.Now().UTC())
}

func (g *Gate) validateCapabilityTokenAt(token *model.CapToken, order model.Order, now time.Time) error {
	if g == nil {
		return fmt.Errorf("risk gate unavailable")
	}
	if token == nil {
		return fmt.Errorf("missing capability token")
	}
	if strings.TrimSpace(token.Nonce) == "" {
		return fmt.Errorf("missing token nonce")
	}
	if strings.TrimSpace(token.Signature) == "" {
		return fmt.Errorf("missing token signature")
	}
	if token.Expiry.IsZero() {
		return fmt.Errorf("missing token expiry")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !now.Before(token.Expiry.UTC()) {
		return fmt.Errorf("capability token expired")
	}
	if token.DeskID != order.DeskID {
		return fmt.Errorf("token desk %q does not match order desk %q", token.DeskID, order.DeskID)
	}
	if token.Capability != order.ExecutionCapability() {
		return fmt.Errorf("token capability %q does not match order capability %q", token.Capability, order.ExecutionCapability())
	}

	expectedSignature, err := g.tokenSignature(order, token.Nonce, token.Expiry.UTC())
	if err != nil {
		return err
	}
	providedSignature, err := hex.DecodeString(token.Signature)
	if err != nil {
		return fmt.Errorf("decode token signature: %w", err)
	}
	if !hmac.Equal(providedSignature, expectedSignature) {
		return fmt.Errorf("capability token signature mismatch")
	}

	if err := validateTokenConstraints(token, order); err != nil {
		return err
	}
	return nil
}

func (g *Gate) tokenSignature(order model.Order, nonce string, expiry time.Time) ([]byte, error) {
	if g == nil || len(g.secret) == 0 {
		return nil, fmt.Errorf("risk token secret unavailable")
	}
	mac := hmac.New(sha256.New, g.secret)
	mac.Write([]byte(tokenSigningData(order, nonce, expiry.UTC())))
	return mac.Sum(nil), nil
}

func tokenSigningData(order model.Order, nonce string, expiry time.Time) string {
	payload := signedCapabilityOrder{
		ID:                  order.ID,
		ThesisID:            order.ThesisID,
		DeskID:              order.DeskID,
		DisplaySymbol:       order.DisplaySymbol(),
		ExecutionCapability: order.ExecutionCapability(),
		Structure:           order.Structure,
		InstrumentKey:       order.Instrument.Key(),
		Legs:                signedCapabilityLegs(order.Legs),
		Direction:           order.Direction,
		Quantity:            order.Quantity,
		OrderType:           order.OrderType,
		LimitPrice:          order.LimitPrice,
		StopPrice:           order.StopPrice,
		TimeInForce:         order.TimeInForce,
		Notional:            order.Notional,
		GrossNotional:       order.GrossNotional(),
		ExpiryUnixNano:      expiry.UTC().UnixNano(),
		Nonce:               nonce,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("%s:%d:%s", order.ID, expiry.UTC().UnixNano(), nonce)
	}
	return string(data)
}

func validateTokenConstraints(token *model.CapToken, order model.Order) error {
	if token == nil {
		return fmt.Errorf("missing capability token")
	}
	if symbol, ok := constraintString(token.Constraints, "symbol"); ok && symbol != order.DisplaySymbol() {
		return fmt.Errorf("token symbol %q does not match order symbol %q", symbol, order.DisplaySymbol())
	}
	if structure, ok := constraintString(token.Constraints, "structure"); ok && structure != order.Structure {
		return fmt.Errorf("token structure %q does not match order structure %q", structure, order.Structure)
	}
	if maxQty, ok := constraintFloat(token.Constraints, "max_qty"); ok && order.Quantity > maxQty+1e-9 {
		return fmt.Errorf("order quantity %.8f exceeds token max %.8f", order.Quantity, maxQty)
	}
	if maxNotional, ok := constraintFloat(token.Constraints, "max_notional"); ok && order.GrossNotional() > maxNotional+1e-6 {
		return fmt.Errorf("order notional %.8f exceeds token max %.8f", order.GrossNotional(), maxNotional)
	}
	return nil
}

type signedCapabilityOrder struct {
	ID                  string                `json:"id"`
	ThesisID            string                `json:"thesis_id"`
	DeskID              string                `json:"desk_id"`
	DisplaySymbol       string                `json:"display_symbol"`
	ExecutionCapability string                `json:"execution_capability"`
	Structure           string                `json:"structure"`
	InstrumentKey       string                `json:"instrument_key"`
	Legs                []signedCapabilityLeg `json:"legs,omitempty"`
	Direction           model.TradeDirection  `json:"direction"`
	Quantity            float64               `json:"quantity"`
	OrderType           model.OrderType       `json:"order_type"`
	LimitPrice          float64               `json:"limit_price"`
	StopPrice           float64               `json:"stop_price"`
	TimeInForce         string                `json:"time_in_force"`
	Notional            float64               `json:"notional"`
	GrossNotional       float64               `json:"gross_notional"`
	ExpiryUnixNano      int64                 `json:"expiry_unix_nano"`
	Nonce               string                `json:"nonce"`
}

type signedCapabilityLeg struct {
	InstrumentKey string               `json:"instrument_key"`
	Direction     model.TradeDirection `json:"direction"`
	Ratio         float64              `json:"ratio"`
	Quantity      float64              `json:"quantity"`
	EntryPrice    float64              `json:"entry_price"`
	CurrentPrice  float64              `json:"current_price"`
	TargetPrice   float64              `json:"target_price"`
	StopLoss      float64              `json:"stop_loss"`
}

func signedCapabilityLegs(legs []model.TradeLeg) []signedCapabilityLeg {
	if len(legs) == 0 {
		return nil
	}
	signed := make([]signedCapabilityLeg, 0, len(legs))
	for _, leg := range legs {
		signed = append(signed, signedCapabilityLeg{
			InstrumentKey: leg.Instrument.Key(),
			Direction:     leg.Direction,
			Ratio:         leg.Ratio,
			Quantity:      leg.Quantity,
			EntryPrice:    leg.EntryPrice,
			CurrentPrice:  leg.CurrentPrice,
			TargetPrice:   leg.TargetPrice,
			StopLoss:      leg.StopLoss,
		})
	}
	return signed
}

func constraintString(values map[string]interface{}, key string) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return "", false
	}
	switch value := raw.(type) {
	case string:
		return value, true
	default:
		return fmt.Sprint(value), true
	}
}

func constraintFloat(values map[string]interface{}, key string) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	default:
		var parsed float64
		if _, err := fmt.Sscan(fmt.Sprint(value), &parsed); err == nil && !math.IsNaN(parsed) {
			return parsed, true
		}
		return 0, false
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
