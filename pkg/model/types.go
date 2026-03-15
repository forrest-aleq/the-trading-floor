package model

import (
	"strconv"
	"time"
)

// Direction for trades
type TradeDirection string

const (
	Long  TradeDirection = "long"
	Short TradeDirection = "short"
)

// Instrument represents any tradeable instrument across all asset classes
type Instrument struct {
	Symbol     string  `json:"symbol"`
	Exchange   string  `json:"exchange,omitempty"`
	SecType    string  `json:"sec_type"` // STK, OPT, FUT, CASH, BOND
	Currency   string  `json:"currency"`
	Expiry     string  `json:"expiry,omitempty"`     // Options/futures
	Strike     float64 `json:"strike,omitempty"`     // Options
	Right      string  `json:"right,omitempty"`      // C or P for options
	Multiplier string  `json:"multiplier,omitempty"` // Contract multiplier
	ConID      int64   `json:"con_id,omitempty"`     // IBKR contract ID
}

func (i Instrument) MultiplierValue() float64 {
	if i.Multiplier != "" {
		if parsed, err := strconv.ParseFloat(i.Multiplier, 64); err == nil && parsed > 0 {
			return parsed
		}
	}

	switch i.SecType {
	case "OPT":
		return 100
	default:
		return 1
	}
}

func (i Instrument) Notional(price, quantity float64) float64 {
	return price * quantity * i.MultiplierValue()
}

// Opportunity is scanner output — a tradeable setup detected from signals
type Opportunity struct {
	ID          string         `json:"id"`
	SignalIDs   []string       `json:"signal_ids"`
	Instruments []Instrument   `json:"instruments"`
	Direction   TradeDirection `json:"direction"`
	Urgency     float64        `json:"urgency"`
	Score       float64        `json:"score"`
	Category    string         `json:"category"`
	CascadeInfo *CascadeInfo   `json:"cascade_info,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type CascadeInfo struct {
	SourceDomain string   `json:"source_domain"`
	TargetGaps   []string `json:"target_gaps"` // Assets that should move but haven't
	Confidence   float64  `json:"confidence"`
	TimeWindow   string   `json:"time_window"`
}

// ThesisStatus tracks lifecycle
type ThesisStatus string

const (
	ThesisEmbryo     ThesisStatus = "embryo"
	ThesisNursery    ThesisStatus = "nursery"
	ThesisProsecuted ThesisStatus = "prosecuted"
	ThesisActive     ThesisStatus = "active"
	ThesisResolved   ThesisStatus = "resolved"
)

// Thesis is the core research output
type Thesis struct {
	ID            string         `json:"id"`
	OpportunityID string         `json:"opportunity_id"`
	DeskID        string         `json:"desk_id"`
	Domain        string         `json:"domain,omitempty"`
	Strategy      string         `json:"strategy"`
	Instrument    Instrument     `json:"instrument"`
	Direction     TradeDirection `json:"direction"`

	Conviction  float64    `json:"conviction"`
	Health      float64    `json:"health"`
	Evidence    []Evidence `json:"evidence"`
	CounterArgs []string   `json:"counter_args"`

	EntryPrice   float64       `json:"entry_price"`
	TargetPrice  float64       `json:"target_price"`
	StopLoss     float64       `json:"stop_loss"`
	PositionSize float64       `json:"position_size"`
	TimeHorizon  time.Duration `json:"time_horizon"`

	KillRules []KillRule   `json:"kill_rules"`
	Status    ThesisStatus `json:"status"`

	AutonomyMode         AutonomyMode    `json:"autonomy_mode,omitempty"`
	ScanTerritory        string          `json:"scan_territory,omitempty"`
	ExecutionTerritory   string          `json:"execution_territory,omitempty"`
	CompetenceKey        string          `json:"competence_key,omitempty"`
	CompetenceTrust      float64         `json:"competence_trust,omitempty"`
	CompetenceConfidence float64         `json:"competence_confidence,omitempty"`
	Prosecution          *Prosecution    `json:"prosecution,omitempty"`
	CouncilVerdict       *CouncilVerdict `json:"council_verdict,omitempty"`
	Outcome              *ThesisOutcome  `json:"outcome,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type Evidence struct {
	Source   string  `json:"source"`
	Content  string  `json:"content"`
	Weight   float64 `json:"weight"`
	SignalID string  `json:"signal_id,omitempty"`
}

type KillRule struct {
	Condition string  `json:"condition"`
	Threshold float64 `json:"threshold"`
	Action    string  `json:"action"` // close, reduce, alert
}

type Prosecution struct {
	Verdict    string   `json:"verdict"` // killed, weakened, survived, strengthened
	BearArgs   []string `json:"bear_args"`
	Analogues  []string `json:"analogues"`
	Confidence float64  `json:"confidence"`
}

type CouncilVerdict struct {
	Approved           bool              `json:"approved"`
	Perspectives       map[string]string `json:"perspectives"` // archetype → view
	AdjustedSize       float64           `json:"adjusted_size,omitempty"`
	AdjustedConviction float64           `json:"adjusted_conviction,omitempty"`
}

type ThesisOutcome struct {
	Profitable   bool    `json:"profitable"`
	RealizedPnL  float64 `json:"realized_pnl"`
	ReturnPct    float64 `json:"return_pct"`
	RiskReward   float64 `json:"risk_reward"`
	HoldingHours float64 `json:"holding_hours"`
	ExitReason   string  `json:"exit_reason"`
	ErrorClass   string  `json:"error_class,omitempty"` // thesis_failure, execution_friction, infrastructure_error, policy_block, market_halt
}

// Order is what gets sent to the risk gate then to IBKR
type Order struct {
	ID          string         `json:"id"`
	ThesisID    string         `json:"thesis_id"`
	DeskID      string         `json:"desk_id"`
	Instrument  Instrument     `json:"instrument"`
	Direction   TradeDirection `json:"direction"`
	Quantity    float64        `json:"quantity"`
	OrderType   OrderType      `json:"order_type"`
	LimitPrice  float64        `json:"limit_price,omitempty"`
	StopPrice   float64        `json:"stop_price,omitempty"`
	TimeInForce string         `json:"time_in_force"` // DAY, GTC, IOC
	Notional    float64        `json:"notional"`
}

type OrderType string

const (
	OrderMarket   OrderType = "MKT"
	OrderLimit    OrderType = "LMT"
	OrderStop     OrderType = "STP"
	OrderStopLmt  OrderType = "STP LMT"
	OrderAdaptive OrderType = "ADAPTIVE"
	OrderTWAP     OrderType = "TWAP"
	OrderMidPrice OrderType = "MIDPRICE"
)

// Fill is what comes back from IBKR after execution
type Fill struct {
	OrderID     string         `json:"order_id"`
	IBKROrderID int64          `json:"ibkr_order_id"`
	Instrument  Instrument     `json:"instrument"`
	Direction   TradeDirection `json:"direction"`
	Quantity    float64        `json:"quantity"`
	AvgPrice    float64        `json:"avg_price"`
	Commission  float64        `json:"commission"`
	FilledAt    time.Time      `json:"filled_at"`
}

// Position is a live position in the book
type Position struct {
	ID             string         `json:"id"`
	ThesisID       string         `json:"thesis_id"`
	DeskID         string         `json:"desk_id"`
	Instrument     Instrument     `json:"instrument"`
	Direction      TradeDirection `json:"direction"`
	Quantity       float64        `json:"quantity"`
	EntryPrice     float64        `json:"entry_price"`
	CurrentPrice   float64        `json:"current_price"`
	UnrealizedPnL  float64        `json:"unrealized_pnl"`
	RealizedPnL    float64        `json:"realized_pnl"`
	IBKROrderID    int64          `json:"ibkr_order_id,omitempty"`
	IBKRContractID int64          `json:"ibkr_contract_id,omitempty"`
	Shadow         bool           `json:"shadow,omitempty"`
	Status         string         `json:"status"` // open, closing, closed
	OpenedAt       time.Time      `json:"opened_at"`
	ClosedAt       *time.Time     `json:"closed_at,omitempty"`
}

// RiskDecision is the output of the risk gate
type RiskDecision struct {
	Allowed       bool        `json:"allowed"`
	Violations    []Violation `json:"violations,omitempty"`
	AdjustedOrder *Order      `json:"adjusted_order,omitempty"`
	Token         *CapToken   `json:"token,omitempty"`
}

type Violation struct {
	Rule    string `json:"rule"`
	Limit   string `json:"limit"`
	Current string `json:"current"`
}

type CapToken struct {
	Capability  string                 `json:"capability"`
	Constraints map[string]interface{} `json:"constraints"`
	DeskID      string                 `json:"desk_id"`
	Expiry      time.Time              `json:"expiry"`
	Nonce       string                 `json:"nonce"`
	Signature   string                 `json:"signature"`
}

// AutonomyMode from MARS
type AutonomyMode string

const (
	Restricted AutonomyMode = "restricted"
	Supervised AutonomyMode = "supervised"
	Autonomous AutonomyMode = "autonomous"
)

// CompetenceState is the belief graph entry
type CompetenceState struct {
	Key          string       `json:"key"`
	DeskID       string       `json:"desk_id"`
	Capability   string       `json:"capability"`
	Context      string       `json:"context,omitempty"`
	Regime       string       `json:"regime,omitempty"`
	Trust        float64      `json:"trust"`
	Confidence   float64      `json:"confidence"`
	SuccessCount int          `json:"success_count"`
	FailureCount int          `json:"failure_count"`
	TotalPnL     float64      `json:"total_pnl"`
	Sharpe       float64      `json:"sharpe"`
	Autonomy     AutonomyMode `json:"autonomy"`
	IsBackfilled bool         `json:"is_backfilled"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// InferAutonomy applies MARS graduation thresholds
func (c *CompetenceState) InferAutonomy() AutonomyMode {
	total := c.SuccessCount + c.FailureCount
	if total == 0 {
		return Restricted
	}
	successRate := float64(c.SuccessCount) / float64(total)
	if c.Trust >= 0.82 && c.Confidence >= 0.70 && successRate >= 0.75 && total >= 100 {
		return Autonomous
	}
	if c.Trust >= 0.65 && c.Confidence >= 0.50 && total >= 50 {
		return Supervised
	}
	return Restricted
}

func (c *CompetenceState) TotalObservations() int {
	return c.SuccessCount + c.FailureCount
}

// Regime represents current market conditions
type Regime struct {
	Volatility string `json:"volatility"` // low, medium, high, extreme
	Trend      string `json:"trend"`      // trending_up, neutral, trending_down
	Risk       string `json:"risk"`       // risk_on, neutral, risk_off
	Liquidity  string `json:"liquidity"`  // normal, stressed, crisis
}

func (r Regime) Key() string {
	return r.Volatility + ":" + r.Trend + ":" + r.Risk + ":" + r.Liquidity
}
