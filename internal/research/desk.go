package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

// Desk orchestrates thesis formation through the trio conversation
type Desk struct {
	log    *slog.Logger
	llm    *llm.Router
	minConviction float64
}

func NewDesk(llmRouter *llm.Router, minConviction float64) *Desk {
	if minConviction == 0 {
		minConviction = 0.65
	}
	return &Desk{
		log:           slog.Default().With("component", "research"),
		llm:           llmRouter,
		minConviction: minConviction,
	}
}

const researchPrompt = `You are a trading research desk. Given this opportunity, build a rigorous trading thesis.

You must determine:
1. INSTRUMENT: What exactly to trade (ticker, option strike/expiry if applicable)
2. DIRECTION: Long or short
3. ENTRY: Target entry price
4. TARGET: Price target (where to take profit)
5. STOP: Stop loss level
6. CONVICTION: 0.0-1.0 how confident you are
7. TIME HORIZON: How long should this trade be held (hours, days, weeks)
8. EVIDENCE: What supports this thesis (list 3-5 pieces of evidence)
9. COUNTER ARGUMENTS: What could go wrong (list 2-3 risks)
10. KILL RULES: Conditions that would invalidate the thesis

Think like Bill Ackman. Deep conviction requires deep analysis. Don't trade unless you have an edge.

Respond in JSON:
{
  "instrument": {"symbol": "...", "sec_type": "STK|OPT|FUT|CASH", "currency": "USD", "exchange": "SMART", "expiry": "", "strike": 0, "right": ""},
  "direction": "long|short",
  "entry_price": 0.0,
  "target_price": 0.0,
  "stop_loss": 0.0,
  "conviction": 0.0,
  "time_horizon_hours": 0,
  "position_size_pct": 0.0,
  "strategy": "scalper|event|macro|fundamental|contrarian|tail",
  "evidence": ["...", "..."],
  "counter_args": ["...", "..."],
  "kill_rules": [{"condition": "...", "threshold": 0.0, "action": "close|reduce|alert"}],
  "reasoning": "..."
}`

// Investigate takes an opportunity and produces a thesis
func (d *Desk) Investigate(ctx context.Context, opp *model.Opportunity, deskID string) (*model.Thesis, error) {
	prompt := fmt.Sprintf("Opportunity (score: %.0f, urgency: %.2f, category: %s):\n\nInstruments: %v\nDirection: %s\nSignal IDs: %v",
		opp.Score, opp.Urgency, opp.Category,
		instrumentNames(opp.Instruments), opp.Direction, opp.SignalIDs,
	)

	if opp.CascadeInfo != nil {
		prompt += fmt.Sprintf("\n\nCascade detected:\n  Source domain: %s\n  Target gaps: %v\n  Confidence: %.2f",
			opp.CascadeInfo.SourceDomain, opp.CascadeInfo.TargetGaps, opp.CascadeInfo.Confidence)
	}

	resp, err := d.llm.AskJSON(ctx, llm.TierAnalysis, researchPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("research LLM error: %w", err)
	}

	var result researchResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, fmt.Errorf("research parse error: %w", err)
	}

	// Build thesis
	evidence := make([]model.Evidence, len(result.Evidence))
	for i, e := range result.Evidence {
		evidence[i] = model.Evidence{Content: e, Weight: 1.0}
	}

	killRules := make([]model.KillRule, len(result.KillRules))
	for i, kr := range result.KillRules {
		killRules[i] = model.KillRule{
			Condition: kr.Condition,
			Threshold: kr.Threshold,
			Action:    kr.Action,
		}
	}

	thesis := &model.Thesis{
		ID:            uuid.New().String(),
		OpportunityID: opp.ID,
		DeskID:        deskID,
		Strategy:      result.Strategy,
		Instrument: model.Instrument{
			Symbol:     result.Instrument.Symbol,
			SecType:    result.Instrument.SecType,
			Currency:   result.Instrument.Currency,
			Exchange:   result.Instrument.Exchange,
			Expiry:     result.Instrument.Expiry,
			Strike:     result.Instrument.Strike,
			Right:      result.Instrument.Right,
		},
		Direction:    model.TradeDirection(result.Direction),
		Conviction:   result.Conviction,
		Health:       0.85, // Initial health
		Evidence:     evidence,
		CounterArgs:  result.CounterArgs,
		EntryPrice:   result.EntryPrice,
		TargetPrice:  result.TargetPrice,
		StopLoss:     result.StopLoss,
		PositionSize: result.PositionSizePct,
		TimeHorizon:  time.Duration(result.TimeHorizonHours) * time.Hour,
		KillRules:    killRules,
		Status:       model.ThesisEmbryo,
		CreatedAt:    time.Now(),
	}

	d.log.Info("thesis formed",
		"id", thesis.ID,
		"desk", deskID,
		"symbol", thesis.Instrument.Symbol,
		"direction", thesis.Direction,
		"conviction", thesis.Conviction,
		"strategy", thesis.Strategy,
	)

	return thesis, nil
}

type researchResult struct {
	Instrument struct {
		Symbol   string  `json:"symbol"`
		SecType  string  `json:"sec_type"`
		Currency string  `json:"currency"`
		Exchange string  `json:"exchange"`
		Expiry   string  `json:"expiry"`
		Strike   float64 `json:"strike"`
		Right    string  `json:"right"`
	} `json:"instrument"`
	Direction        string   `json:"direction"`
	EntryPrice       float64  `json:"entry_price"`
	TargetPrice      float64  `json:"target_price"`
	StopLoss         float64  `json:"stop_loss"`
	Conviction       float64  `json:"conviction"`
	TimeHorizonHours int      `json:"time_horizon_hours"`
	PositionSizePct  float64  `json:"position_size_pct"`
	Strategy         string   `json:"strategy"`
	Evidence         []string `json:"evidence"`
	CounterArgs      []string `json:"counter_args"`
	KillRules        []struct {
		Condition string  `json:"condition"`
		Threshold float64 `json:"threshold"`
		Action    string  `json:"action"`
	} `json:"kill_rules"`
	Reasoning string `json:"reasoning"`
}

func instrumentNames(instruments []model.Instrument) []string {
	names := make([]string, len(instruments))
	for i, inst := range instruments {
		names[i] = inst.Symbol
	}
	return names
}
