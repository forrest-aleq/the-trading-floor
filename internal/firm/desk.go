package firm

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/store"
	"github.com/hnic/trading-floor/internal/trace"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

// Desk is a permanent trio (Trader + Analyst + Researcher) covering a domain.
type Desk struct {
	ID          string
	Domain      string
	ABGroup     string // "A" (full MARS) or "B" (control)
	Capital     float64
	log         *slog.Logger
	llm         *llm.Router
	scanner     *scanner.Engine
	research    *research.Desk
	prosecutor  *research.Prosecutor
	council     *research.Council
	riskGate    *risk.Gate
	execution   *execution.Manager
	book        *book.Book
	beliefs     *belief.Graph
	learnWorker *memory.LearnWorker
	engrams     *memory.EngramStore
	store       *store.DB
	onTrade     func()

	minConviction    float64
	councilThreshold float64
	regime           model.Regime

	mu           sync.RWMutex
	activeTheses map[string]*model.Thesis
}

type DeskConfig struct {
	ID      string
	Domain  string
	ABGroup string
	Capital float64

	LLM         *llm.Router
	Scanner     *scanner.Engine
	Research    *research.Desk
	Prosecutor  *research.Prosecutor
	Council     *research.Council
	RiskGate    *risk.Gate
	Execution   *execution.Manager
	Book        *book.Book
	Beliefs     *belief.Graph
	LearnWorker *memory.LearnWorker
	Engrams     *memory.EngramStore
	Store       *store.DB
	OnTrade     func()

	MinConviction    float64
	CouncilThreshold float64
}

func NewDesk(cfg DeskConfig) *Desk {
	if cfg.MinConviction == 0 {
		cfg.MinConviction = 0.65
	}
	if cfg.CouncilThreshold == 0 {
		cfg.CouncilThreshold = 0.02
	}

	if cfg.Book != nil && cfg.Capital > 0 {
		cfg.Book.SetDeskCapital(cfg.ID, cfg.Capital)
	}

	return &Desk{
		ID:               cfg.ID,
		Domain:           cfg.Domain,
		ABGroup:          cfg.ABGroup,
		Capital:          cfg.Capital,
		log:              slog.Default().With("component", "desk", "desk_id", cfg.ID),
		llm:              cfg.LLM,
		scanner:          cfg.Scanner,
		research:         cfg.Research,
		prosecutor:       cfg.Prosecutor,
		council:          cfg.Council,
		riskGate:         cfg.RiskGate,
		execution:        cfg.Execution,
		book:             cfg.Book,
		beliefs:          cfg.Beliefs,
		learnWorker:      cfg.LearnWorker,
		engrams:          cfg.Engrams,
		store:            cfg.Store,
		onTrade:          cfg.OnTrade,
		minConviction:    cfg.MinConviction,
		councilThreshold: cfg.CouncilThreshold,
		regime: model.Regime{
			Volatility: "medium",
			Trend:      "neutral",
			Risk:       "neutral",
			Liquidity:  "normal",
		},
		activeTheses: make(map[string]*model.Thesis),
	}
}

// Process handles a single signal through the full pipeline.
func (d *Desk) Process(ctx context.Context, sig signal.Signal) {
	span := trace.FromContext(ctx).WithStage("scanner")
	ctx = trace.IntoContext(ctx, span)

	d.persistSignal(ctx, sig)

	opp, ok := d.scanner.Evaluate(ctx, sig, d.Domain)
	if !ok {
		return
	}

	d.persistOpportunity(ctx, opp)

	d.log.Info("opportunity detected",
		append(span.Fields(), "score", opp.Score, "urgency", opp.Urgency, "category", opp.Category)...,
	)

	span = span.WithStage("research")
	ctx = trace.IntoContext(ctx, span)

	thesis, err := d.research.Investigate(ctx, opp, d.ID)
	if err != nil {
		d.log.Warn("research failed", append(span.Fields(), "error", err)...)
		return
	}

	d.maybeSpawnSubTeam(ctx, thesis)

	// Engram lookup: boost conviction if we have a cached winning play for this pattern
	if d.ABGroup == "A" && d.engrams != nil {
		intentKey := thesis.Strategy + "_" + thesis.Instrument.SecType
		engrams := d.engrams.Lookup(intentKey, d.ID)
		for _, eg := range engrams {
			if eg.TotalObservations() >= 5 && eg.WinRate() > 0.6 {
				boost := (eg.WinRate() - 0.5) * 0.1 // max +0.05 boost
				thesis.Conviction += boost
				if thesis.Conviction > 1.0 {
					thesis.Conviction = 1.0
				}
				d.log.Info("engram boost applied",
					"intent", intentKey,
					"win_rate", eg.WinRate(),
					"boost", boost,
					"new_conviction", thesis.Conviction,
				)
				break
			}
		}
	}

	d.persistThesis(ctx, thesis)

	if thesis.Conviction < d.minConviction {
		d.log.Info("thesis below conviction threshold",
			"conviction", thesis.Conviction,
			"threshold", d.minConviction,
		)
		d.recordAntiPortfolio(ctx, thesis, "conviction_below_threshold")
		return
	}

	span = span.WithStage("prosecutor")
	ctx = trace.IntoContext(ctx, span)

	prosecution := d.prosecutor.Challenge(ctx, thesis)
	thesis.Prosecution = prosecution
	thesis.Status = model.ThesisProsecuted
	d.persistThesis(ctx, thesis)

	if prosecution.Verdict == "killed" {
		d.log.Info("thesis killed by prosecutor",
			append(span.Fields(), "thesis_id", thesis.ID, "bear_args", len(prosecution.BearArgs))...,
		)
		d.recordAntiPortfolio(ctx, thesis, "killed_by_prosecutor")
		return
	}

	thesis.Conviction += prosecution.Confidence
	if thesis.Conviction > 1.0 {
		thesis.Conviction = 1.0
	}
	if thesis.Conviction < d.minConviction {
		d.log.Info("thesis weakened below threshold by prosecutor",
			"conviction", thesis.Conviction,
		)
		d.recordAntiPortfolio(ctx, thesis, "prosecutor_weakened_below_threshold")
		return
	}

	// Council debate for large positions
	if d.council != nil && thesis.PositionSize > d.councilThreshold {
		span = span.WithStage("council")
		ctx = trace.IntoContext(ctx, span)

		verdict := d.council.Debate(ctx, thesis)
		thesis.CouncilVerdict = verdict
		d.persistThesis(ctx, thesis)
		if !verdict.Approved {
			d.log.Info("thesis rejected by council",
				"thesis_id", thesis.ID,
				"perspectives", len(verdict.Perspectives),
			)
			d.recordAntiPortfolio(ctx, thesis, "council_rejected")
			return
		}
		thesis.Conviction = verdict.AdjustedConviction
		thesis.PositionSize = verdict.AdjustedSize
		d.log.Info("council approved",
			"thesis_id", thesis.ID,
			"adjusted_conviction", verdict.AdjustedConviction,
			"adjusted_size", verdict.AdjustedSize,
		)
		d.persistThesis(ctx, thesis)
	}

	span = span.WithStage("risk")
	ctx = trace.IntoContext(ctx, span)
	d.normalizePositionSize(thesis)

	order := model.Order{
		ID:          thesis.ID,
		ThesisID:    thesis.ID,
		DeskID:      d.ID,
		Instrument:  thesis.Instrument,
		Direction:   thesis.Direction,
		Quantity:    thesis.PositionSize,
		OrderType:   model.OrderLimit,
		LimitPrice:  thesis.EntryPrice,
		StopPrice:   thesis.StopLoss,
		TimeInForce: "DAY",
		Notional:    thesis.Instrument.Notional(thesis.EntryPrice, thesis.PositionSize),
	}

	snapshot := d.book.Snapshot()
	portfolioState := risk.PortfolioState{
		NAV:           snapshot.NAV,
		Cash:          snapshot.Cash,
		GrossExposure: snapshot.GrossExposure,
		NetExposure:   snapshot.NetExposure,
		DailyPnL:      snapshot.DailyPnL,
		MonthlyPnL:    snapshot.MonthlyPnL,
		OpenPositions: snapshot.OpenPositions,
		DeskPositions: snapshot.DeskPositions,
		DeskDailyPnL:  snapshot.DeskPnL,
		DeskCapital:   snapshot.DeskCapital,
	}

	decision := d.riskGate.Check(order, thesis, portfolioState)
	if !decision.Allowed {
		d.log.Info("thesis blocked by risk gate",
			"thesis_id", thesis.ID,
			"violations", len(decision.Violations),
		)
		d.recordAntiPortfolio(ctx, thesis, "blocked_by_risk_gate")
		return
	}

	span = span.WithStage("execution")
	ctx = trace.IntoContext(ctx, span)

	fill, err := d.execution.Submit(ctx, decision.Token, *decision.AdjustedOrder)
	if err != nil {
		d.log.Error("execution failed", "thesis_id", thesis.ID, "error", err)
		return
	}

	span = span.WithStage("book")
	ctx = trace.IntoContext(ctx, span)

	pos := d.book.OpenPosition(fill, thesis)
	thesis.Status = model.ThesisActive
	d.rememberThesis(thesis)
	d.persistThesis(ctx, thesis)
	d.persistPosition(ctx, pos)

	if d.onTrade != nil {
		d.onTrade()
	}

	d.log.Info("trade executed",
		"thesis_id", thesis.ID,
		"symbol", fill.Instrument.Symbol,
		"direction", fill.Direction,
		"price", fill.AvgPrice,
		"quantity", fill.Quantity,
		"conviction", thesis.Conviction,
		"strategy", thesis.Strategy,
		"desk", d.ID,
		"ab_group", d.ABGroup,
		"time", time.Now().Format(time.RFC3339),
	)
}

func (d *Desk) ProcessOutcome(ctx context.Context, thesis *model.Thesis, outcome *model.ThesisOutcome) {
	if thesis == nil || outcome == nil {
		return
	}

	now := time.Now()
	thesis.Status = model.ThesisResolved
	thesis.Outcome = outcome
	thesis.ResolvedAt = &now

	d.persistThesis(ctx, thesis)
	d.forgetThesis(thesis.ID)

	if d.ABGroup == "B" {
		d.log.Info("outcome recorded (control group, no belief update)",
			"thesis_id", thesis.ID,
			"profitable", outcome.Profitable,
			"pnl", outcome.RealizedPnL,
		)
		return
	}

	if d.learnWorker != nil {
		d.learnWorker.ProcessOutcome(thesis, outcome, d.regime)
	}

	d.log.Info("outcome processed",
		"thesis_id", thesis.ID,
		"profitable", outcome.Profitable,
		"pnl", outcome.RealizedPnL,
		"strategy", thesis.Strategy,
	)
}

func (d *Desk) GetThesis(id string) (*model.Thesis, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	thesis, ok := d.activeTheses[id]
	return thesis, ok
}

func (d *Desk) SetRegime(regime model.Regime) {
	d.regime = regime
}

func (d *Desk) rememberThesis(thesis *model.Thesis) {
	d.mu.Lock()
	d.activeTheses[thesis.ID] = thesis
	d.mu.Unlock()
}

func (d *Desk) forgetThesis(thesisID string) {
	d.mu.Lock()
	delete(d.activeTheses, thesisID)
	d.mu.Unlock()
}

func (d *Desk) maybeSpawnSubTeam(ctx context.Context, thesis *model.Thesis) {
	if d.llm == nil || thesis == nil {
		return
	}

	intent, shouldSpawn := ShouldSpawnSubTeam(thesis)
	if !shouldSpawn {
		return
	}

	agents, ok := DefaultSubTeamConfigs()[intent]
	if !ok || len(agents) == 0 {
		d.log.Warn("sub-team config missing", "intent", intent, "thesis_id", thesis.ID)
		return
	}

	result := SpawnSubTeam(ctx, d.llm, SubTeamConfig{
		DeskID:   d.ID,
		Purpose:  intent,
		Agents:   agents,
		Deadline: 30 * time.Minute,
	})
	if result == nil {
		return
	}

	// Attach sub-team output to the thesis so it affects downstream review.
	for role, analysis := range result.Analyses {
		if analysis == "" {
			continue
		}
		thesis.Evidence = append(thesis.Evidence, model.Evidence{
			Source:  "subteam:" + role,
			Content: analysis,
			Weight:  0.7,
		})
	}
	if result.Consensus != "" {
		thesis.Evidence = append(thesis.Evidence, model.Evidence{
			Source:  "subteam:consensus:" + intent,
			Content: result.Consensus,
			Weight:  0.9,
		})
	}

	d.log.Info("sub-team evidence merged",
		"thesis_id", thesis.ID,
		"intent", intent,
		"analyses", len(result.Analyses),
	)
}

func (d *Desk) normalizePositionSize(thesis *model.Thesis) {
	if thesis == nil || thesis.PositionSize <= 0 || thesis.EntryPrice <= 0 || d.Capital <= 0 {
		return
	}

	targetNotional := d.Capital * thesis.PositionSize
	unitNotional := thesis.Instrument.Notional(thesis.EntryPrice, 1)
	if targetNotional <= 0 || unitNotional <= 0 {
		return
	}

	quantity := targetNotional / unitNotional
	switch thesis.Instrument.SecType {
	case "OPT", "FUT":
		quantity = math.Max(1, math.Floor(quantity))
	}
	thesis.PositionSize = quantity
}

func (d *Desk) persistSignal(ctx context.Context, sig signal.Signal) {
	if d.store == nil {
		return
	}
	if err := d.store.UpsertSignal(ctx, sig); err != nil {
		d.log.Warn("persist signal failed", "signal_id", sig.ID, "error", err)
	}
}

func (d *Desk) persistOpportunity(ctx context.Context, opp *model.Opportunity) {
	if d.store == nil || opp == nil {
		return
	}
	if err := d.store.UpsertOpportunity(ctx, opp); err != nil {
		d.log.Warn("persist opportunity failed", "opportunity_id", opp.ID, "error", err)
	}
}

func (d *Desk) persistThesis(ctx context.Context, thesis *model.Thesis) {
	if d.store == nil || thesis == nil {
		return
	}
	if err := d.store.UpsertThesis(ctx, thesis); err != nil {
		d.log.Warn("persist thesis failed", "thesis_id", thesis.ID, "error", err)
	}
}

func (d *Desk) persistPosition(ctx context.Context, pos *model.Position) {
	if d.store == nil || pos == nil {
		return
	}
	if err := d.store.UpsertPosition(ctx, pos); err != nil {
		d.log.Warn("persist position failed", "position_id", pos.ID, "error", err)
	}
}

func (d *Desk) recordAntiPortfolio(ctx context.Context, thesis *model.Thesis, reason string) {
	if d.store == nil || thesis == nil {
		return
	}
	if err := d.store.InsertAntiPortfolio(ctx, thesis, reason); err != nil {
		d.log.Warn("persist anti-portfolio failed", "thesis_id", thesis.ID, "error", err)
	}
}
