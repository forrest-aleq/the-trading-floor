package firm

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/store"
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
	scanner     *scanner.Engine
	research    *research.Desk
	prosecutor  *research.Prosecutor
	riskGate    *risk.Gate
	execution   *execution.Manager
	book        *book.Book
	beliefs     *belief.Graph
	learnWorker *memory.LearnWorker
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

	Scanner     *scanner.Engine
	Research    *research.Desk
	Prosecutor  *research.Prosecutor
	RiskGate    *risk.Gate
	Execution   *execution.Manager
	Book        *book.Book
	Beliefs     *belief.Graph
	LearnWorker *memory.LearnWorker
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
		scanner:          cfg.Scanner,
		research:         cfg.Research,
		prosecutor:       cfg.Prosecutor,
		riskGate:         cfg.RiskGate,
		execution:        cfg.Execution,
		book:             cfg.Book,
		beliefs:          cfg.Beliefs,
		learnWorker:      cfg.LearnWorker,
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
	d.persistSignal(ctx, sig)

	opp, ok := d.scanner.Evaluate(ctx, sig, d.Domain)
	if !ok {
		return
	}

	d.persistOpportunity(ctx, opp)

	d.log.Info("opportunity detected",
		"score", opp.Score,
		"urgency", opp.Urgency,
		"category", opp.Category,
	)

	thesis, err := d.research.Investigate(ctx, opp, d.ID)
	if err != nil {
		d.log.Warn("research failed", "error", err)
		return
	}
	d.normalizePositionSize(thesis)
	d.persistThesis(ctx, thesis)

	if thesis.Conviction < d.minConviction {
		d.log.Info("thesis below conviction threshold",
			"conviction", thesis.Conviction,
			"threshold", d.minConviction,
		)
		d.recordAntiPortfolio(ctx, thesis, "conviction_below_threshold")
		return
	}

	prosecution := d.prosecutor.Challenge(ctx, thesis)
	thesis.Prosecution = prosecution
	thesis.Status = model.ThesisProsecuted
	d.persistThesis(ctx, thesis)

	if prosecution.Verdict == "killed" {
		d.log.Info("thesis killed by prosecutor",
			"thesis_id", thesis.ID,
			"bear_args", len(prosecution.BearArgs),
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

	fill, err := d.execution.Submit(ctx, decision.Token, *decision.AdjustedOrder)
	if err != nil {
		d.log.Error("execution failed", "thesis_id", thesis.ID, "error", err)
		return
	}

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

func (d *Desk) normalizePositionSize(thesis *model.Thesis) {
	if thesis == nil || thesis.PositionSize <= 0 || thesis.EntryPrice <= 0 || d.Capital <= 0 {
		return
	}

	if thesis.PositionSize > 1 {
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
