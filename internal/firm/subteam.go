package firm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/pkg/model"
)

// SubTeam is a temporary group of LLM agents spawned for deep-dive research.
// Created when a thesis needs deeper analysis (e.g., complex M&A, macro regime shift).
// Disbanded after producing its deliverable.
type SubTeam struct {
	ID        string
	DeskID    string
	Purpose   string
	Agents    []SubAgent
	CreatedAt time.Time
	Deadline  time.Time

	log *slog.Logger
	llm *llm.Router
}

const (
	subTeamAgentTimeout    = 30 * time.Second
	subTeamSynthesisTimout = 40 * time.Second
	subTeamAgentMaxTokens  = 512
	subTeamSynthMaxTokens  = 768
	subTeamBudgetBuffer    = 5 * time.Second
)

// SubAgent is a single specialist agent within a sub-team.
type SubAgent struct {
	Role   string // e.g. "sector_analyst", "risk_modeler", "catalyst_tracker"
	Prompt string // System prompt defining this agent's specialty
}

// SubTeamResult is the combined output from all sub-team agents.
type SubTeamResult struct {
	SubTeamID string
	DeskID    string
	Analyses  map[string]string // role -> analysis
	Consensus string            // synthesized recommendation
	Duration  time.Duration
}

// SubTeamConfig defines what kind of sub-team to spawn.
type SubTeamConfig struct {
	DeskID   string
	Purpose  string
	Agents   []SubAgent
	Deadline time.Duration
}

// DefaultSubTeamConfigs returns pre-defined sub-team templates.
func DefaultSubTeamConfigs() map[string][]SubAgent {
	return map[string][]SubAgent{
		"large_position_review": {
			{Role: "risk_analyst", Prompt: "Stress test the downside of this oversized trade. Focus on gap risk, liquidity, stop slippage, and how this position interacts with existing portfolio exposures."},
			{Role: "liquidity_analyst", Prompt: "Evaluate whether this position size is executable. Focus on average daily volume, spread, slippage, options open interest, and exit feasibility under stress."},
			{Role: "scenario_analyst", Prompt: "Map the three most likely scenarios over the holding period. Quantify upside, base, and downside cases and identify the catalyst path for each."},
		},
		"earnings_deep_dive": {
			{Role: "fundamental_analyst", Prompt: "Analyze the company's financial statements, margins, growth trajectory, and valuation multiples. Compare to sector peers. Identify whether earnings quality is improving or deteriorating."},
			{Role: "options_analyst", Prompt: "Analyze the options chain around this earnings event. Look at implied vol vs realized, put/call ratios, unusual activity, and expected move. Identify the best risk/reward structure for expressing this view."},
			{Role: "sentiment_analyst", Prompt: "Analyze analyst consensus, recent estimate revisions, whisper numbers, and retail sentiment. Is the market positioned for a beat or miss? Where is the surprise potential?"},
		},
		"macro_regime_shift": {
			{Role: "rates_analyst", Prompt: "Analyze the rate environment: Fed policy, yield curve shape, real rates, and rate expectations. How does this regime shift affect rate-sensitive sectors and duration exposure?"},
			{Role: "cross_asset_analyst", Prompt: "Analyze cross-asset correlations in this new regime. How are equities, bonds, commodities, and FX responding? Which traditional correlations are breaking?"},
			{Role: "positioning_analyst", Prompt: "Analyze fund flows, CFTC positioning, ETF flows, and margin debt. Where is the market over/under-positioned for this regime change? Where will forced liquidations occur?"},
		},
		"geopolitical_crisis": {
			{Role: "supply_chain_analyst", Prompt: "Map the supply chain disruptions from this event. Which companies have direct exposure? Which have second-order exposure through suppliers? What substitutes exist?"},
			{Role: "commodity_analyst", Prompt: "Analyze commodity price implications. Which energy, metal, or agricultural commodities are affected? What are the supply/demand dynamics? How do inventories look?"},
			{Role: "historical_analyst", Prompt: "Find historical analogues to this geopolitical event. How did markets react? What was the timeline from crisis to recovery? Which sectors led and lagged?"},
		},
		"mna_analysis": {
			{Role: "deal_analyst", Prompt: "Analyze the deal structure: premium offered, financing (cash vs stock), regulatory hurdles, timeline, and completion probability. Compare to recent comparable deals."},
			{Role: "arb_analyst", Prompt: "Evaluate the merger arbitrage opportunity. Current spread, expected timeline, downside if deal breaks, probability-weighted return. Factor in financing costs."},
			{Role: "antitrust_analyst", Prompt: "Assess regulatory risk. Which agencies have jurisdiction? Historical precedents for similar deals? Are there market concentration concerns? Political headwinds?"},
		},
	}
}

// SpawnSubTeam creates and immediately runs a sub-team.
func SpawnSubTeam(ctx context.Context, llmRouter *llm.Router, cfg SubTeamConfig) *SubTeamResult {
	st := &SubTeam{
		ID:        uuid.New().String()[:8],
		DeskID:    cfg.DeskID,
		Purpose:   cfg.Purpose,
		Agents:    cfg.Agents,
		CreatedAt: time.Now(),
		Deadline:  time.Now().Add(cfg.Deadline),
		log:       slog.Default().With("component", "subteam", "subteam_id", uuid.New().String()[:8], "desk_id", cfg.DeskID),
		llm:       llmRouter,
	}

	return st.run(ctx)
}

func (st *SubTeam) run(ctx context.Context) *SubTeamResult {
	start := time.Now()
	ctx = llm.WithUsageContext(ctx, llm.UsageContext{
		DeskID: st.DeskID,
		Stage:  "subteam",
	})
	st.log.Info("sub-team spawned", "purpose", st.Purpose, "agents", len(st.Agents))

	// Run all agents in parallel
	var wg sync.WaitGroup
	analyses := make(map[string]string)
	var mu sync.Mutex

	for _, agent := range st.Agents {
		wg.Add(1)
		go func(a SubAgent) {
			defer wg.Done()

			prompt := fmt.Sprintf("You are a %s on a temporary research sub-team. Purpose: %s\n\nProvide your analysis in 2-3 paragraphs. Be specific with data points, names, and numbers.", a.Role, st.Purpose)

			callCtx, cancel := context.WithTimeout(ctx, subTeamAgentTimeout)
			callCtx = llm.WithUsageContext(callCtx, llm.UsageContext{Stage: "subteam:" + a.Role})
			defer cancel()

			resp, err := st.llm.AskWithLimit(callCtx, llm.TierAnalysis, a.Prompt, prompt, subTeamAgentMaxTokens, 0.4)
			if err != nil {
				st.log.Warn("sub-team agent failed", "role", a.Role, "error", err)
				return
			}

			mu.Lock()
			analyses[a.Role] = resp
			mu.Unlock()
		}(agent)
	}

	wg.Wait()

	// Synthesize
	consensus := st.synthesize(ctx, analyses)

	result := &SubTeamResult{
		SubTeamID: st.ID,
		DeskID:    st.DeskID,
		Analyses:  analyses,
		Consensus: consensus,
		Duration:  time.Since(start),
	}

	st.log.Info("sub-team completed",
		"purpose", st.Purpose,
		"agents_responded", len(analyses),
		"duration", result.Duration,
	)

	return result
}

func (st *SubTeam) synthesize(ctx context.Context, analyses map[string]string) string {
	if len(analyses) == 0 {
		return "no agent analyses available"
	}

	var prompt string
	for role, analysis := range analyses {
		prompt += fmt.Sprintf("--- %s ---\n%s\n\n", role, analysis)
	}

	systemPrompt := "You are a research director synthesizing analyses from your sub-team. Combine their perspectives into a unified recommendation. Highlight areas of agreement and disagreement. Provide a clear actionable conclusion in 2-3 paragraphs."

	callCtx, cancel := context.WithTimeout(ctx, subTeamSynthesisTimout)
	callCtx = llm.WithUsageContext(callCtx, llm.UsageContext{Stage: "subteam:synthesis"})
	defer cancel()

	resp, err := st.llm.AskWithLimit(callCtx, llm.TierAnalysis, systemPrompt, prompt, subTeamSynthMaxTokens, 0.4)
	if err != nil {
		st.log.Warn("sub-team synthesis failed", "error", err)
		return "synthesis failed"
	}

	return resp
}

// ShouldSpawnSubTeam determines whether a thesis warrants deep-dive research.
func ShouldSpawnSubTeam(thesis *model.Thesis) (string, bool) {
	// Large positions (>3% of portfolio suggested size)
	if thesis.PositionSize > 0.03 {
		return "large_position_review", true
	}

	// Earnings events
	if thesis.Strategy == "event" || thesis.Strategy == "earnings" {
		return "earnings_deep_dive", true
	}

	// M&A / special situations
	if thesis.Strategy == "mna" || thesis.Strategy == "special_sits" {
		return "mna_analysis", true
	}

	return "", false
}

func subTeamRequiredBudget() time.Duration {
	return subTeamAgentTimeout + subTeamSynthesisTimout + subTeamBudgetBuffer
}
