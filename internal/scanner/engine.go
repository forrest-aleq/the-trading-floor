package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

// Engine evaluates signals for tradeable opportunities using the speed-tier LLM
type Engine struct {
	log      *slog.Logger
	llm      *llm.Router
	minScore float64 // Minimum score to pass (0-100)
}

func NewEngine(llmRouter *llm.Router, minScore float64) *Engine {
	if minScore == 0 {
		minScore = 70 // Default: aggressive filter — most signals should be rejected
	}
	return &Engine{
		log:      slog.Default().With("component", "scanner"),
		llm:      llmRouter,
		minScore: minScore,
	}
}

const scannerPrompt = `You are a trading signal scanner. Your DEFAULT response should be tradeable: false. Most signals are noise.

Only mark tradeable: true if ALL of these are met:
1. There is a SPECIFIC, actionable trade thesis (not vague commentary)
2. You can name EXACT instruments to trade (tickers, not sectors)
3. There is a clear catalyst with a defined time window
4. The signal contains hard data or a confirmed event (not rumor or speculation)
5. The expected move is large enough to overcome transaction costs

If in doubt, set tradeable: false. We lose nothing by passing. We lose real money by acting on noise.

Respond in JSON:
{
  "tradeable": true/false,
  "score": 0-100,
  "instruments": [{"symbol": "AAPL", "sec_type": "STK", "currency": "USD"}],
  "direction": "long" or "short",
  "urgency": 0.0-1.0,
  "category": "geopolitical|macro|corporate|flows|tail|volatility|sector|systematic",
  "reasoning": "brief explanation"
}`

// Evaluate checks if a signal contains a tradeable opportunity
func (e *Engine) Evaluate(ctx context.Context, sig signal.Signal, domain string) (*model.Opportunity, bool) {
	content := formatSignal(sig)

	prompt := fmt.Sprintf("Domain filter: %s\n\nSignal:\n%s", domain, content)

	resp, err := e.llm.AskJSON(ctx, llm.TierSpeed, scannerPrompt, prompt)
	if err != nil {
		e.log.Warn("scanner LLM error", "error", err, "signal_id", sig.ID)
		return nil, false
	}

	cleaned, err := llm.ExtractJSON(resp)
	if err != nil {
		e.log.Warn("scanner JSON extraction failed", "error", err, "signal_id", sig.ID)
		return nil, false
	}

	var result scanResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		e.log.Warn("scanner parse error", "error", err, "response", cleaned)
		return nil, false
	}

	if !result.Tradeable || result.Score < e.minScore {
		return nil, false
	}

	// Build instruments
	instruments := make([]model.Instrument, len(result.Instruments))
	for i, inst := range result.Instruments {
		instruments[i] = model.Instrument{
			Symbol:   inst.Symbol,
			SecType:  inst.SecType,
			Currency: inst.Currency,
			Exchange: "SMART", // IBKR smart routing default
		}
		if instruments[i].Currency == "" {
			instruments[i].Currency = "USD"
		}
		if instruments[i].SecType == "" {
			instruments[i].SecType = "STK"
		}
	}

	direction := model.Long
	if result.Direction == "short" {
		direction = model.Short
	}

	opp := &model.Opportunity{
		ID:          uuid.New().String(),
		SignalIDs:   []string{sig.ID},
		Instruments: instruments,
		Direction:   direction,
		Urgency:     result.Urgency,
		Score:       result.Score,
		Category:    result.Category,
		CreatedAt:   time.Now(),
	}

	e.log.Info("opportunity detected",
		"score", result.Score,
		"instruments", len(instruments),
		"direction", direction,
		"category", result.Category,
		"signal_source", sig.Source,
	)

	return opp, true
}

type scanResult struct {
	Tradeable   bool    `json:"tradeable"`
	Score       float64 `json:"score"`
	Instruments []struct {
		Symbol   string `json:"symbol"`
		SecType  string `json:"sec_type"`
		Currency string `json:"currency"`
	} `json:"instruments"`
	Direction string  `json:"direction"`
	Urgency   float64 `json:"urgency"`
	Category  string  `json:"category"`
	Reasoning string  `json:"reasoning"`
}

func formatSignal(sig signal.Signal) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Source: %s\n", sig.Source))
	sb.WriteString(fmt.Sprintf("Type: %s\n", sig.Type))
	sb.WriteString(fmt.Sprintf("Category: %s\n", sig.Category))
	sb.WriteString(fmt.Sprintf("Urgency: %.2f\n", sig.Urgency))
	if sig.Translated != "" {
		sb.WriteString(fmt.Sprintf("Content: %s\n", sig.Translated))
	} else if len(sig.Raw) > 0 {
		sb.WriteString(fmt.Sprintf("Content: %s\n", string(sig.Raw)))
	}
	if len(sig.Entities) > 0 {
		names := make([]string, len(sig.Entities))
		for i, e := range sig.Entities {
			names[i] = e.Name
		}
		sb.WriteString(fmt.Sprintf("Entities: %s\n", strings.Join(names, ", ")))
	}
	return sb.String()
}
