package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

// Council convenes multiple strategy archetypes to debate large positions (>2% of portfolio).
// Each archetype evaluates independently, then the council synthesizes.
type Council struct {
	log        *slog.Logger
	llm        *llm.Router
	archetypes []Archetype
}

const (
	councilPerspectiveMaxTokens = 384
	councilPerspectiveTimeout   = 25 * time.Second
)

type perspectiveResult struct {
	name       string
	view       string
	conviction float64
	size       float64
}

// Archetype represents a strategic perspective for council debate.
type Archetype struct {
	Name   string // e.g. "Fundamental", "Contrarian", "Macro", "Tail", "Scalper"
	Prompt string // System prompt defining this archetype's perspective
}

func NewCouncil(llmRouter *llm.Router) *Council {
	return &Council{
		log: slog.Default().With("component", "council"),
		llm: llmRouter,
		archetypes: []Archetype{
			{
				Name: "Fundamental",
				Prompt: `You are a fundamental analyst on the trading council. Evaluate this thesis purely on numbers and fundamentals.
Ask: Do the financials support this? What are the valuation multiples? Is growth priced in? What do margins look like?
Respond in JSON: {"perspective": "...", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Contrarian",
				Prompt: `You are the contrarian voice on the trading council. Your job is to check if this trade is already crowded.
Ask: Is everyone already positioned this way? Is this the obvious trade? What happens if the crowd reverses? Where is the pain trade?
Respond in JSON: {"perspective": "...", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Macro",
				Prompt: `You are the macro strategist on the trading council. Evaluate whether the macro regime supports this thesis.
Ask: Does the rate environment help or hurt? What is the vol regime? Is risk appetite expanding or contracting? Does this trade fight the Fed?
Respond in JSON: {"perspective": "...", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Tail",
				Prompt: `You are the tail risk analyst on the trading council. Your job is to find the worst case scenario.
Ask: What kills this trade? What is the max loss? Is there gap risk? What black swan event invalidates the thesis? Is the risk/reward actually asymmetric?
Respond in JSON: {"perspective": "...", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
			{
				Name: "Timing",
				Prompt: `You are the timing/execution specialist on the trading council. Evaluate whether the entry timing is right.
Ask: Is the market trending or mean-reverting? Are we chasing? Is there a better entry? What does the order flow look like? Should we wait for a pullback?
Respond in JSON: {"perspective": "...", "conviction_adjustment": -0.2 to +0.2, "size_adjustment": 0.5 to 1.5, "reasoning": "..."}`,
			},
		},
	}
}

// Debate convenes all archetypes to evaluate a thesis in parallel.
func (c *Council) Debate(ctx context.Context, thesis *model.Thesis) *model.CouncilVerdict {
	thesisPrompt := fmt.Sprintf(`Thesis under council review:

Symbol: %s (%s)
Direction: %s
Strategy: %s
Conviction: %.2f
Entry: %.2f / Target: %.2f / Stop: %.2f
Time Horizon: %s
Position Size (notional %%): %.2f

Evidence: %s
Counter Arguments: %s
Prosecution Verdict: %s
Quant Metrics:
%s`,
		thesis.Instrument.Symbol, thesis.Instrument.SecType,
		thesis.Direction, thesis.Strategy,
		thesis.Conviction,
		thesis.EntryPrice, thesis.TargetPrice, thesis.StopLoss,
		thesis.TimeHorizon,
		thesis.PositionSize,
		formatEvidence(thesis.Evidence),
		formatCounterArgs(thesis.CounterArgs),
		prosecutionVerdict(thesis.Prosecution),
		formatQuantMetrics(thesis.QuantMetrics),
	)

	var mu sync.Mutex
	var results []perspectiveResult
	var wg sync.WaitGroup

	for _, arch := range c.archetypes {
		wg.Add(1)
		go func(a Archetype) {
			defer wg.Done()

			callCtx, cancel := context.WithTimeout(ctx, councilPerspectiveTimeout)
			defer cancel()

			resp, err := c.llm.AskJSONWithLimit(callCtx, llm.TierCritical, a.Prompt, thesisPrompt, councilPerspectiveMaxTokens, 0.2)
			if err != nil {
				c.log.Warn("council archetype failed", "archetype", a.Name, "error", err)
				return
			}

			cleaned, err := llm.ExtractJSON(resp)
			if err != nil {
				c.log.Warn("council JSON extraction failed",
					"archetype", a.Name,
					"error", err,
					"response_len", len(resp),
					"response_excerpt", truncateForLog(resp, 320),
				)
				return
			}

			var pr struct {
				Perspective          string  `json:"perspective"`
				ConvictionAdjustment float64 `json:"conviction_adjustment"`
				SizeAdjustment       float64 `json:"size_adjustment"`
				Reasoning            string  `json:"reasoning"`
			}
			if err := json.Unmarshal([]byte(cleaned), &pr); err != nil {
				c.log.Warn("council parse failed",
					"archetype", a.Name,
					"error", err,
					"response_excerpt", truncateForLog(cleaned, 320),
				)
				return
			}

			mu.Lock()
			results = append(results, perspectiveResult{
				name:       a.Name,
				view:       pr.Perspective,
				conviction: pr.ConvictionAdjustment,
				size:       pr.SizeAdjustment,
			})
			mu.Unlock()
		}(arch)
	}

	wg.Wait()

	return c.synthesize(thesis, results)
}

func (c *Council) synthesize(thesis *model.Thesis, results []perspectiveResult) *model.CouncilVerdict {
	if len(results) == 0 {
		return &model.CouncilVerdict{
			Approved: false,
			Perspectives: map[string]string{
				"error": "no archetypes responded",
			},
		}
	}

	perspectives := make(map[string]string, len(results))
	totalConvAdj := 0.0
	totalSizeAdj := 0.0

	for _, r := range results {
		perspectives[r.name] = r.view
		totalConvAdj += r.conviction
		totalSizeAdj += r.size
	}

	avgConvAdj := totalConvAdj / float64(len(results))
	avgSizeAdj := totalSizeAdj / float64(len(results))

	if avgSizeAdj <= 0 {
		avgSizeAdj = 1.0
	}

	adjustedConviction := thesis.Conviction + avgConvAdj
	if adjustedConviction > 1.0 {
		adjustedConviction = 1.0
	}
	if adjustedConviction < 0 {
		adjustedConviction = 0
	}

	adjustedSize := thesis.PositionSize * avgSizeAdj

	// Approved if majority didn't strongly oppose (avg conviction adj > -0.1)
	approved := avgConvAdj > -0.1

	c.log.Info("council verdict",
		"thesis_id", thesis.ID,
		"approved", approved,
		"avg_conviction_adj", avgConvAdj,
		"avg_size_adj", avgSizeAdj,
		"perspectives", len(results),
	)

	return &model.CouncilVerdict{
		Approved:           approved,
		Perspectives:       perspectives,
		AdjustedSize:       adjustedSize,
		AdjustedConviction: adjustedConviction,
	}
}

func prosecutionVerdict(p *model.Prosecution) string {
	if p == nil {
		return "not prosecuted"
	}
	return p.Verdict
}
