package firm

import (
	"context"
	"log/slog"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

// Desk is a permanent trio (Trader + Analyst + Researcher) covering a domain
type Desk struct {
	ID             string
	Domain         string
	ABGroup        string // "A" (full MARS) or "B" (control)
	Capital        float64

	log            *slog.Logger
	scanner        *scanner.Engine
	research       *research.Desk
	prosecutor     *research.Prosecutor
	riskGate       *risk.Gate
	execution      *execution.Manager
	book           *book.Book
	beliefs        *belief.Graph
	learnWorker    *memory.LearnWorker

	// Config
	minConviction  float64
	councilThreshold float64
	regime         model.Regime
}

type DeskConfig struct {
	ID       string
	Domain   string
	ABGroup  string
	Capital  float64

	Scanner    *scanner.Engine
	Research   *research.Desk
	Prosecutor *research.Prosecutor
	RiskGate   *risk.Gate
	Execution  *execution.Manager
	Book       *book.Book
	Beliefs    *belief.Graph
	LearnWorker *memory.LearnWorker

	MinConviction    float64
	CouncilThreshold float64
}

func NewDesk(cfg DeskConfig) *Desk {
	if cfg.MinConviction == 0 {
		cfg.MinConviction = 0.65
	}
	if cfg.CouncilThreshold == 0 {
		cfg.CouncilThreshold = 0.02 // 2% of portfolio triggers council
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
		minConviction:    cfg.MinConviction,
		councilThreshold: cfg.CouncilThreshold,
	}
}

// Process handles a single signal through the full pipeline
func (d *Desk) Process(ctx context.Context, sig signal.Signal) {
	// 1. SCAN: Is this relevant and tradeable?
	opp, ok := d.scanner.Evaluate(ctx, sig, d.Domain)
	if !ok {
		return
	}

	d.log.Info("opportunity detected",
		"score", opp.Score,
		"urgency", opp.Urgency,
		"category", opp.Category,
	)

	// 2. RESEARCH: Build thesis
	thesis, err := d.research.Investigate(ctx, opp, d.ID)
	if err != nil {
		d.log.Warn("research failed", "error", err)
		return
	}

	if thesis.Conviction < d.minConviction {
		d.log.Info("thesis below conviction threshold",
			"conviction", thesis.Conviction,
			"threshold", d.minConviction,
		)
		// TODO: record in anti-portfolio
		return
	}

	// 3. PROSECUTE: Try to kill it (Claude Sonnet)
	prosecution := d.prosecutor.Challenge(ctx, thesis)
	thesis.Prosecution = prosecution

	if prosecution.Verdict == "killed" {
		d.log.Info("thesis killed by prosecutor",
			"thesis_id", thesis.ID,
			"bear_args", len(prosecution.BearArgs),
		)
		// TODO: record in anti-portfolio
		return
	}

	// Adjust conviction based on prosecution
	thesis.Conviction += prosecution.Confidence
	if thesis.Conviction > 1.0 {
		thesis.Conviction = 1.0
	}
	if thesis.Conviction < d.minConviction {
		d.log.Info("thesis weakened below threshold by prosecutor",
			"conviction", thesis.Conviction,
		)
		return
	}

	// 4. RISK: Deterministic check
	order := model.Order{
		ID:          thesis.ID,
		ThesisID:    thesis.ID,
		DeskID:      d.ID,
		Instrument:  thesis.Instrument,
		Direction:   thesis.Direction,
		Quantity:     thesis.PositionSize,
		OrderType:   model.OrderLimit,
		LimitPrice:  thesis.EntryPrice,
		TimeInForce: "DAY",
		Notional:    thesis.EntryPrice * thesis.PositionSize,
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
		// TODO: record in anti-portfolio
		return
	}

	// 5. EXECUTE: Submit to IBKR
	fill, err := d.execution.Submit(ctx, decision.Token, *decision.AdjustedOrder)
	if err != nil {
		d.log.Error("execution failed", "thesis_id", thesis.ID, "error", err)
		return
	}

	// 6. BOOK: Record position
	d.book.OpenPosition(fill, thesis)

	// 7. REMEMBER: Record episode start
	d.log.Info("TRADE EXECUTED",
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

	thesis.Status = model.ThesisActive
}

// SetRegime updates the current market regime for this desk
func (d *Desk) SetRegime(regime model.Regime) {
	d.regime = regime
}
