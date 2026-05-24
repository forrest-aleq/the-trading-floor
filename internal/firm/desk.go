package firm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	kalshiexec "github.com/hnic/trading-floor/internal/execution/kalshi"
	"github.com/hnic/trading-floor/internal/graphdb"
	"github.com/hnic/trading-floor/internal/institutional"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/orderflow"
	"github.com/hnic/trading-floor/internal/quant"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/store"
	"github.com/hnic/trading-floor/internal/trace"
	"github.com/hnic/trading-floor/pkg/evidence"
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
	compiler    *orderflow.Compiler
	quant       *quant.Service
	prosecutor  *research.Prosecutor
	council     *research.Council
	riskGate    *risk.Gate
	execution   *execution.Manager
	kalshi      *kalshiexec.Executor
	book        *book.Book
	beliefs     *belief.Graph
	learnWorker *memory.LearnWorker
	engrams     *memory.EngramStore
	store       *store.DB
	graph       *graphdb.Client
	onTrade     func()
	watchlist   func([]model.Instrument)
	publish     func(context.Context, signal.Signal) error
	entryCtl    EntryControl

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

	LLM           *llm.Router
	Scanner       *scanner.Engine
	Research      *research.Desk
	Compiler      *orderflow.Compiler
	Quant         *quant.Service
	Prosecutor    *research.Prosecutor
	Council       *research.Council
	RiskGate      *risk.Gate
	Execution     *execution.Manager
	Kalshi        *kalshiexec.Executor
	Book          *book.Book
	Beliefs       *belief.Graph
	LearnWorker   *memory.LearnWorker
	Engrams       *memory.EngramStore
	Store         *store.DB
	Graph         *graphdb.Client
	OnTrade       func()
	Watchlist     func([]model.Instrument)
	PublishSignal func(context.Context, signal.Signal) error
	EntryControl  EntryControl

	MinConviction    float64
	CouncilThreshold float64
}

var (
	deskScannerTimeout    = readDeskDurationEnv("DESK_SCANNER_TIMEOUT", 20*time.Second)
	deskResearchTimeout   = readDeskDurationEnv("DESK_RESEARCH_TIMEOUT", 45*time.Second)
	deskProsecutionTimout = readDeskDurationEnv("DESK_PROSECUTION_TIMEOUT", 35*time.Second)
	deskCouncilTimeout    = readDeskDurationEnv("DESK_COUNCIL_TIMEOUT", 45*time.Second)
	deskExecutionTimeout  = readDeskDurationEnv("DESK_EXECUTION_TIMEOUT", 30*time.Second)
	deskSlowStageWarnAt   = readDeskDurationEnv("DESK_SLOW_STAGE_WARN_AT", 10*time.Second)
	deskColleagueWeight   = readDeskFloatEnv("DESK_COLLEAGUE_TRUST_WEIGHT", 0.18)
	deskSubTeamsEnabled   = readDeskBoolEnv("DESK_ENABLE_SUBTEAMS", true)
)

func ReloadRuntimeConfig() {
	deskScannerTimeout = readDeskDurationEnv("DESK_SCANNER_TIMEOUT", 20*time.Second)
	deskResearchTimeout = readDeskDurationEnv("DESK_RESEARCH_TIMEOUT", 45*time.Second)
	deskProsecutionTimout = readDeskDurationEnv("DESK_PROSECUTION_TIMEOUT", 35*time.Second)
	deskCouncilTimeout = readDeskDurationEnv("DESK_COUNCIL_TIMEOUT", 45*time.Second)
	deskExecutionTimeout = readDeskDurationEnv("DESK_EXECUTION_TIMEOUT", 30*time.Second)
	deskSlowStageWarnAt = readDeskDurationEnv("DESK_SLOW_STAGE_WARN_AT", 10*time.Second)
	deskColleagueWeight = readDeskFloatEnv("DESK_COLLEAGUE_TRUST_WEIGHT", 0.18)
	deskSubTeamsEnabled = readDeskBoolEnv("DESK_ENABLE_SUBTEAMS", true)
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
		compiler:         firstCompiler(cfg.Compiler),
		quant:            cfg.Quant,
		prosecutor:       cfg.Prosecutor,
		council:          cfg.Council,
		riskGate:         cfg.RiskGate,
		execution:        cfg.Execution,
		kalshi:           cfg.Kalshi,
		book:             cfg.Book,
		beliefs:          cfg.Beliefs,
		learnWorker:      cfg.LearnWorker,
		engrams:          cfg.Engrams,
		store:            cfg.Store,
		graph:            cfg.Graph,
		onTrade:          cfg.OnTrade,
		watchlist:        cfg.Watchlist,
		publish:          cfg.PublishSignal,
		entryCtl:         cfg.EntryControl,
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

func firstCompiler(compiler *orderflow.Compiler) *orderflow.Compiler {
	if compiler != nil {
		return compiler
	}
	return orderflow.NewCompiler()
}

// Process handles a single signal through the full pipeline.
func (d *Desk) Process(ctx context.Context, sig signal.Signal) {
	if err := d.graph.RecordSignalSeen(ctx, sig.ID, d.ID, d.Domain, time.Now().UTC()); err != nil {
		d.log.Warn("graph signal seen failed", "signal_id", sig.ID, "error", err)
	}
	sig = d.augmentSignalInstitutionalState(sig)

	span := trace.FromContext(ctx).WithStage("scanner")
	ctx = trace.IntoContext(ctx, span)
	scanTerritory := d.assessScanTerritory()

	stageStart := time.Now()
	scanCtx, scanCancel := context.WithTimeout(ctx, deskScannerTimeout)
	scanEval := d.scanner.EvaluateDetailed(scanCtx, sig, d.Domain)
	scanCancel()
	opp, ok := scanEval.Opportunity, scanEval.Accepted
	d.logStage("scanner", stageStart,
		"signal_id", sig.ID,
		"tradeable", ok,
		"scanner_reason", scanEval.Reason,
		"scanner_score", scanEval.Score,
		"scan_territory", scanTerritory.Status,
	)
	if !ok {
		d.log.Info("signal rejected by scanner",
			"signal_id", sig.ID,
			"reason", scanEval.Reason,
			"score", scanEval.Score,
			"tradeable", scanEval.Tradeable,
			"source", sig.Source,
			"category", sig.Category,
			"type", sig.Type,
			"urgency", sig.Urgency,
		)
		d.recordScannerRejection(ctx, sig, scanEval)
		return
	}

	d.persistOpportunity(ctx, opp)
	if allowed, reason := opp.EvidenceGate(); !allowed {
		d.log.Info("opportunity blocked by evidence gate",
			"signal_id", sig.ID,
			"opportunity_id", opp.ID,
			"reason", reason,
			"source_trust", evidenceTrustValue(opp.EvidenceMeta),
			"evidence_score", evidenceScoreValue(opp.EvidenceMeta),
		)
		return
	}

	d.log.Info("opportunity detected",
		append(span.Fields(), "score", opp.Score, "urgency", opp.Urgency, "category", opp.Category)...,
	)

	span = span.WithStage("research")
	ctx = trace.IntoContext(ctx, span)

	stageStart = time.Now()
	researchCtx, researchCancel := context.WithTimeout(ctx, deskResearchTimeout)
	thesis, err := d.research.Investigate(researchCtx, opp, sig, d.ID)
	researchCancel()
	d.logStage("research", stageStart,
		"signal_id", sig.ID,
		"opportunity_id", opp.ID,
	)
	if err != nil {
		d.log.Warn("research failed", append(span.Fields(), "error", err)...)
		return
	}
	thesis.Domain = d.Domain
	d.applyCollaborationContext(sig, thesis)
	if d.quant != nil {
		thesis.QuantMetrics = d.quant.AnalyzeThesis(thesis)
		if thesis.QuantMetrics != nil {
			d.log.Info("quant metrics attached",
				"thesis_id", thesis.ID,
				"method", thesis.QuantMetrics.Method,
				"defined_risk", thesis.QuantMetrics.DefinedRisk,
				"max_loss", thesis.QuantMetrics.MaxLoss,
				"reward_to_risk", thesis.QuantMetrics.RewardToRisk,
				"warnings", len(thesis.QuantMetrics.Warnings),
			)
		}
	}

	d.maybeSpawnSubTeam(ctx, thesis)

	// Engram lookup: boost conviction if we have a cached winning play for this pattern
	if d.ABGroup == "A" && d.engrams != nil {
		intentKey := thesis.Strategy + "_" + thesis.ExecutionCapability()
		regimeKey := d.regime.Key()
		engrams := d.engrams.LookupContext(
			intentKey,
			d.ID,
			thesis.DisplaySymbol()+"_"+regimeKey,
			thesis.Strategy+"_"+regimeKey,
		)
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
	d.recordCollaborationInput(ctx, thesis, sig)

	if thesis.Conviction < d.minConviction {
		d.log.Info("thesis below conviction threshold",
			"conviction", thesis.Conviction,
			"threshold", d.minConviction,
		)
		d.recordAntiPortfolio(ctx, thesis, "conviction_below_threshold")
		return
	}

	autonomy := d.resolveAutonomy(scanTerritory, thesis)
	d.applyAutonomy(thesis, autonomy)
	d.log.Info("autonomy resolved",
		"thesis_id", thesis.ID,
		"symbol", thesis.DisplaySymbol(),
		"mode", autonomy.Mode,
		"reason", autonomy.Reason,
		"scan_territory", autonomy.ScanTerritory.Status,
		"execution_territory", autonomy.ExecTerritory.Status,
		"competence_key", autonomy.CompetenceKey,
	)
	d.persistThesis(ctx, thesis)

	span = span.WithStage("prosecutor")
	ctx = trace.IntoContext(ctx, span)

	stageStart = time.Now()
	prosecutionCtx, prosecutionCancel := context.WithTimeout(ctx, deskProsecutionTimout)
	prosecution := d.prosecutor.Challenge(prosecutionCtx, thesis)
	prosecutionCancel()
	d.logStage("prosecutor", stageStart,
		"thesis_id", thesis.ID,
		"verdict", prosecution.Verdict,
	)
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
	requiresCouncil := d.council != nil && (thesis.PositionSize > d.councilThreshold || autonomy.Mode == model.Supervised)
	if requiresCouncil {
		span = span.WithStage("council")
		ctx = trace.IntoContext(ctx, span)

		stageStart = time.Now()
		councilCtx, councilCancel := context.WithTimeout(ctx, deskCouncilTimeout)
		verdict := d.council.Debate(councilCtx, thesis)
		councilCancel()
		d.logStage("council", stageStart,
			"thesis_id", thesis.ID,
			"approved", verdict.Approved,
		)
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
	if autonomy.Mode == model.Supervised {
		thesis.PositionSize *= 0.5
	}
	if policy := d.currentEntryPolicy(); !policy.AllowEntries {
		d.log.Warn("entry blocked by runtime health policy",
			"thesis_id", thesis.ID,
			"symbol", thesis.DisplaySymbol(),
			"mode", policy.Mode,
			"reason", policy.Reason,
			"updated_at", policy.UpdatedAt,
		)
		if policy.Reason != "" {
			thesis.CounterArgs = append(thesis.CounterArgs, "runtime_health:"+policy.Reason)
		} else {
			thesis.CounterArgs = append(thesis.CounterArgs, "runtime_health:entries_disabled")
		}
		d.persistThesis(ctx, thesis)
		d.recordAntiPortfolio(ctx, thesis, "blocked_by_runtime_health")
		return
	}
	if d.isPredictionMarketDesk() {
		if !d.handlesKalshiThesis() {
			d.log.Warn("prediction-market thesis rejected; Kalshi executor is unavailable",
				"thesis_id", thesis.ID,
				"symbol", thesis.DisplaySymbol(),
				"desk", d.ID,
			)
			d.recordAntiPortfolio(ctx, thesis, "kalshi_executor_unavailable")
			return
		}
		d.handleKalshiThesis(ctx, thesis, autonomy)
		return
	}
	d.research.HydrateThesisPricing(ctx, thesis)
	d.normalizePositionSize(thesis)

	order, err := d.compiler.CompileEntry(orderflow.EntryInput{
		DeskID: d.ID,
		Thesis: thesis,
	})
	if err != nil {
		d.log.Warn("compile order failed", "thesis_id", thesis.ID, "error", err)
		return
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

	decision := d.riskGate.Check(*order, thesis, portfolioState)
	if !decision.Allowed {
		d.log.Info("thesis blocked by risk gate",
			"thesis_id", thesis.ID,
			"violations", len(decision.Violations),
		)
		d.recordAntiPortfolio(ctx, thesis, "blocked_by_risk_gate")
		return
	}
	if d.watchlist != nil {
		d.watchlist(thesis.ExecutionInstruments())
	}
	d.maybePublishInternalSignal(ctx, sig, thesis)

	span = span.WithStage("book")
	ctx = trace.IntoContext(ctx, span)

	var pos *model.Position
	if autonomy.Mode == model.Restricted {
		pos = d.book.OpenShadowPosition(thesis)
		thesis.Status = model.ThesisNursery
		d.log.Info("thesis routed to shadow book",
			"thesis_id", thesis.ID,
			"symbol", thesis.DisplaySymbol(),
			"scan_territory", thesis.ScanTerritory,
			"execution_territory", thesis.ExecutionTerritory,
			"autonomy_mode", thesis.AutonomyMode,
		)
	} else {
		span = span.WithStage("execution")
		ctx = trace.IntoContext(ctx, span)

		stageStart = time.Now()
		executionCtx, executionCancel := context.WithTimeout(ctx, deskExecutionTimeout)
		fill, err := d.execution.Submit(executionCtx, decision.Token, *decision.AdjustedOrder)
		executionCancel()
		d.logStage("execution", stageStart,
			"thesis_id", thesis.ID,
			"symbol", thesis.DisplaySymbol(),
		)
		if err != nil {
			var pending *execution.PendingFillError
			if errors.As(err, &pending) {
				d.MarkPendingExecution(ctx, thesis, pending)
			} else if d.execution != nil && d.execution.IsPaper() && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
				d.log.Warn("paper execution timed out before broker confirmation",
					"thesis_id", thesis.ID,
					"symbol", thesis.DisplaySymbol(),
					"error", err,
				)
				return
			} else {
				d.log.Error("execution failed", "thesis_id", thesis.ID, "error", err)
				return
			}
		} else {
			span = span.WithStage("book")
			ctx = trace.IntoContext(ctx, span)
			pos = d.book.ApplyExecutionFill(fill, thesis)
			thesis.Status = model.ThesisActive
		}
	}
	d.rememberThesis(thesis)
	d.persistThesis(ctx, thesis)
	d.persistPosition(ctx, pos)

	if d.onTrade != nil && pos != nil && !pos.Shadow {
		d.onTrade()
	}

	if pos == nil {
		d.log.Info("order staged for live broker execution",
			"thesis_id", thesis.ID,
			"symbol", thesis.DisplaySymbol(),
			"desk", d.ID,
			"ab_group", d.ABGroup,
			"autonomy_mode", thesis.AutonomyMode,
			"status", thesis.Status,
			"time", time.Now().Format(time.RFC3339),
		)
		return
	}

	d.log.Info("trade executed",
		"thesis_id", thesis.ID,
		"symbol", pos.DisplaySymbol(),
		"direction", pos.Direction,
		"price", pos.EntryPrice,
		"quantity", pos.Quantity,
		"conviction", thesis.Conviction,
		"strategy", thesis.Strategy,
		"desk", d.ID,
		"ab_group", d.ABGroup,
		"autonomy_mode", thesis.AutonomyMode,
		"shadow", pos.Shadow,
		"time", time.Now().Format(time.RFC3339),
	)
}

func (d *Desk) MarkPendingExecution(ctx context.Context, thesis *model.Thesis, pending *execution.PendingFillError) {
	if d == nil || thesis == nil || pending == nil {
		return
	}
	thesis.Status = model.ThesisPending
	d.rememberThesis(thesis)
	d.persistThesis(ctx, thesis)
	d.log.Warn("execution pending at broker; thesis left out of book until fill reconciliation",
		"thesis_id", thesis.ID,
		"symbol", thesis.DisplaySymbol(),
		"broker_order_id", pending.OrderID,
		"order_status", pending.Status,
	)
}

func (d *Desk) currentEntryPolicy() EntryPolicy {
	if d == nil || d.entryCtl == nil {
		return NormalEntryPolicy(time.Now().UTC())
	}
	policy := d.entryCtl.CurrentEntryPolicy()
	if policy.Mode == "" {
		return NormalEntryPolicy(time.Now().UTC())
	}
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = time.Now().UTC()
	}
	return policy
}

func (d *Desk) handlesKalshiThesis() bool {
	return d != nil && d.kalshi != nil && strings.EqualFold(strings.TrimSpace(d.Domain), "prediction_market")
}

func (d *Desk) isPredictionMarketDesk() bool {
	return d != nil && strings.EqualFold(strings.TrimSpace(d.Domain), "prediction_market")
}

func (d *Desk) handleKalshiThesis(ctx context.Context, thesis *model.Thesis, autonomy autonomyDecision) {
	if d == nil || thesis == nil || d.kalshi == nil {
		return
	}

	if autonomy.Mode == model.Restricted && !d.kalshi.IsDryRun() {
		thesis.Status = model.ThesisNursery
		d.rememberThesis(thesis)
		d.persistThesis(ctx, thesis)
		d.log.Info("kalshi live order blocked by restricted autonomy",
			"thesis_id", thesis.ID,
			"symbol", thesis.DisplaySymbol(),
			"autonomy_mode", thesis.AutonomyMode,
		)
		return
	}

	executionCtx, cancel := context.WithTimeout(ctx, deskExecutionTimeout)
	defer cancel()
	result, err := d.kalshi.SubmitThesis(executionCtx, thesis)
	if err != nil {
		d.log.Warn("kalshi thesis mapping/execution failed",
			"thesis_id", thesis.ID,
			"symbol", thesis.DisplaySymbol(),
			"error", err,
		)
		d.recordAntiPortfolio(ctx, thesis, "kalshi_execution_rejected")
		return
	}

	switch {
	case result.DryRun:
		thesis.Status = model.ThesisNursery
	case result.Response != nil && result.Response.HasFill():
		thesis.Status = model.ThesisActive
	case result.Response != nil && result.Response.IsResting():
		thesis.Status = model.ThesisPending
		d.log.Info("kalshi order resting without fill",
			"thesis_id", thesis.ID,
			"order_id", result.Response.OrderID,
			"status", result.Response.Status,
		)
	case result.Response != nil:
		thesis.Status = model.ThesisProsecuted
		d.recordAntiPortfolio(ctx, thesis, "kalshi_order_not_filled")
	default:
		thesis.Status = model.ThesisPending
	}
	d.rememberThesis(thesis)
	d.persistThesis(ctx, thesis)

	if d.onTrade != nil && !result.DryRun && thesis.Status == model.ThesisActive {
		d.onTrade()
	}

	d.log.Info("kalshi order mapped",
		"thesis_id", thesis.ID,
		"desk", d.ID,
		"mode", result.Mode,
		"dry_run", result.DryRun,
		"ticker", result.MappedOrder.Request.Ticker,
		"side", result.MappedOrder.Request.Side,
		"action", result.MappedOrder.Request.Action,
		"price", result.MappedOrder.Request.PriceDollars(),
		"count", result.MappedOrder.Request.Count,
		"estimated_risk", kalshiexec.FormatCents(result.MappedOrder.EstimatedRiskCents),
		"filled_count", filledKalshiCount(result.Response),
		"order_status", kalshiOrderStatus(result.Response),
		"autonomy_mode", thesis.AutonomyMode,
		"status", thesis.Status,
	)
}

func filledKalshiCount(resp *kalshiexec.OrderResponse) float64 {
	if resp == nil {
		return 0
	}
	return resp.FilledCount()
}

func kalshiOrderStatus(resp *kalshiexec.OrderResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Status
}

func (d *Desk) RecordExecutionFill(ctx context.Context, fill *model.Fill) (*model.Position, error) {
	if d == nil || fill == nil {
		return nil, errors.New("nil desk or fill")
	}
	thesis, ok := d.GetThesis(fill.OrderID)
	if !ok && d.store != nil {
		loaded, err := d.store.GetThesis(ctx, fill.OrderID)
		if err != nil {
			return nil, err
		}
		thesis = loaded
	}
	if thesis == nil {
		return nil, fmt.Errorf("thesis %s not found for execution fill", fill.OrderID)
	}

	pos := d.book.ApplyExecutionFill(fill, thesis)
	thesis.Status = model.ThesisActive
	d.rememberThesis(thesis)
	d.persistThesis(ctx, thesis)
	d.persistPosition(ctx, pos)
	if d.onTrade != nil && pos != nil && !pos.Shadow {
		d.onTrade()
	}

	d.log.Info("reconciled broker fill into book",
		"thesis_id", thesis.ID,
		"symbol", pos.DisplaySymbol(),
		"price", pos.EntryPrice,
		"quantity", pos.Quantity,
		"broker_order_id", fill.IBKROrderID,
	)
	return pos, nil
}

func (d *Desk) ResolvePendingExecution(ctx context.Context, orderID string, state execution.OrderState, brokerStatus string) {
	if d == nil || orderID == "" {
		return
	}
	thesis, ok := d.GetThesis(orderID)
	if !ok && d.store != nil {
		loaded, err := d.store.GetThesis(ctx, orderID)
		if err == nil {
			thesis = loaded
		}
	}
	if thesis == nil || thesis.Status != model.ThesisPending {
		return
	}
	if state == execution.OrderStateFilled {
		return
	}

	thesis.Status = model.ThesisProsecuted
	d.rememberThesis(thesis)
	d.persistThesis(ctx, thesis)
	d.log.Warn("pending execution resolved without position activation",
		"thesis_id", thesis.ID,
		"state", state,
		"broker_status", brokerStatus,
	)
}

func evidenceTrustValue(meta *evidence.Metadata) float64 {
	if meta == nil {
		return 0
	}
	return meta.SourceTrust
}

func evidenceScoreValue(meta *evidence.Metadata) float64 {
	if meta == nil {
		return 0
	}
	return meta.EvidenceScore
}

func readDeskDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func readDeskFloatEnv(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func readDeskBoolEnv(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func (d *Desk) applyCollaborationContext(sig signal.Signal, thesis *model.Thesis) {
	input := d.collaborationInputForSignal(sig)
	institutional.ApplyCollaborationInput(thesis, input, 0.55, deskColleagueWeight)
}

func (d *Desk) collaborationInputForSignal(sig signal.Signal) *model.CollaborationInput {
	return institutional.CollaborationInputForSignal(sig, func(originDesk, originDomain string) (*model.DeskRelationshipBelief, bool) {
		if d.beliefs == nil || originDesk == "" {
			return nil, false
		}
		peer, ok := d.beliefs.LookupPeer(originDesk, d.ID, firstNonEmptyInternal(d.Domain, originDomain), d.regime.Key())
		if !ok {
			return nil, false
		}
		return peer, true
	})
}

func (d *Desk) augmentSignalInstitutionalState(sig signal.Signal) signal.Signal {
	input := d.collaborationInputForSignal(sig)
	sig = institutional.AugmentSignalWithCollaborationContext(sig, input)
	return institutional.EnrichSignalCognition(sig, d.Domain, input)
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
	if !deskSubTeamsEnabled || d.llm == nil || thesis == nil {
		return
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < subTeamRequiredBudget() {
			d.log.Info("skipping sub-team due to remaining task budget",
				"thesis_id", thesis.ID,
				"remaining", remaining,
				"required", subTeamRequiredBudget(),
			)
			return
		}
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

func (d *Desk) maybePublishInternalSignal(ctx context.Context, origin signal.Signal, thesis *model.Thesis) {
	if d.publish == nil || thesis == nil {
		return
	}
	internal, ok := buildInternalSignal(origin, thesis, d.ID)
	if !ok {
		return
	}
	if err := d.publish(ctx, internal); err != nil {
		d.log.Warn("publish internal thesis signal failed",
			"thesis_id", thesis.ID,
			"origin_source", origin.Source,
			"error", err,
		)
		return
	}
	d.log.Info("published internal thesis signal",
		"thesis_id", thesis.ID,
		"signal_id", internal.ID,
		"targets", internalTargetDomains(internal),
	)
}

func (d *Desk) normalizePositionSize(thesis *model.Thesis) {
	if thesis == nil || thesis.PositionSize <= 0 || d.Capital <= 0 {
		return
	}
	referencePrice := thesisReferencePrice(thesis)
	if referencePrice <= 0 {
		return
	}

	targetNotional := d.Capital * thesis.PositionSize
	originalEntry := thesis.EntryPrice
	if originalEntry <= 0 {
		thesis.EntryPrice = referencePrice
	}
	unitNotional := thesis.GrossEntryNotional(1)
	thesis.EntryPrice = originalEntry
	if targetNotional <= 0 || unitNotional <= 0 {
		return
	}

	quantity := targetNotional / unitNotional
	switch thesis.PrimaryInstrument().SecType {
	case "OPT", "FUT", "FOP":
		quantity = math.Max(1, math.Floor(quantity))
	}
	thesis.PositionSize = quantity
}

func thesisReferencePrice(thesis *model.Thesis) float64 {
	if thesis == nil {
		return 0
	}
	candidates := []float64{thesis.EntryPrice}
	if thesis.MarketContext != nil {
		candidates = append(candidates, thesis.MarketContext.CurrentPrice)
	}
	candidates = append(candidates, thesis.TargetPrice, thesis.StopLoss)
	for _, price := range candidates {
		if price > 0 {
			return price
		}
	}
	return 0
}

type autonomyDecision struct {
	Mode          model.AutonomyMode
	ScanTerritory belief.TerritoryAssessment
	ExecTerritory belief.TerritoryAssessment
	CompetenceKey string
	Reason        string
}

func (d *Desk) assessScanTerritory() belief.TerritoryAssessment {
	if d.ABGroup != "A" || d.beliefs == nil {
		return belief.TerritoryAssessment{Status: belief.TerritoryKnown}
	}
	return d.beliefs.AssessTerritory(d.ID, "scan", d.Domain, d.regime.Key(), 20)
}

func (d *Desk) resolveAutonomy(scanTerritory belief.TerritoryAssessment, thesis *model.Thesis) autonomyDecision {
	if thesis == nil {
		return autonomyDecision{Mode: model.Autonomous}
	}

	capability := thesis.ExecutionCapability()
	key := belief.CompetenceKey(d.ID, thesis.Strategy, capability, d.regime.Key())
	if d.ABGroup != "A" || d.beliefs == nil {
		return autonomyDecision{
			Mode:          model.Autonomous,
			ScanTerritory: scanTerritory,
			ExecTerritory: belief.TerritoryAssessment{Status: belief.TerritoryKnown},
			CompetenceKey: key,
			Reason:        "control_group",
		}
	}

	execTerritory := d.beliefs.AssessTerritory(d.ID, thesis.Strategy, capability, d.regime.Key(), 20)
	decision := autonomyDecision{
		Mode:          model.Restricted,
		ScanTerritory: scanTerritory,
		ExecTerritory: execTerritory,
		CompetenceKey: key,
		Reason:        "unknown_territory",
	}

	switch {
	case scanTerritory.Status == belief.TerritoryUnknown:
		decision.Reason = "unknown_scan_territory"
	case scanTerritory.Status == belief.TerritoryAdjacent:
		decision.Mode = model.Supervised
		decision.Reason = "adjacent_scan_territory"
	case execTerritory.Status == belief.TerritoryAdjacent:
		decision.Mode = model.Supervised
		decision.Reason = "adjacent_execution_territory"
	case execTerritory.Status == belief.TerritoryKnown && execTerritory.Exact != nil:
		decision.Mode = execTerritory.Exact.Autonomy
		decision.Reason = "earned_autonomy"
	default:
		decision.Reason = "unknown_execution_territory"
	}

	return decision
}

func (d *Desk) applyAutonomy(thesis *model.Thesis, decision autonomyDecision) {
	if thesis == nil {
		return
	}

	thesis.AutonomyMode = decision.Mode
	thesis.ScanTerritory = string(decision.ScanTerritory.Status)
	thesis.ExecutionTerritory = string(decision.ExecTerritory.Status)
	thesis.CompetenceKey = decision.CompetenceKey

	if decision.ExecTerritory.Exact != nil {
		thesis.CompetenceTrust = decision.ExecTerritory.Exact.Trust
		thesis.CompetenceConfidence = decision.ExecTerritory.Exact.Confidence
		enrichThesisCompetenceConfidence(thesis)
		return
	}
	if decision.ScanTerritory.Exact != nil {
		thesis.CompetenceTrust = decision.ScanTerritory.Exact.Trust
		thesis.CompetenceConfidence = decision.ScanTerritory.Exact.Confidence
		enrichThesisCompetenceConfidence(thesis)
	}
}

func enrichThesisCompetenceConfidence(thesis *model.Thesis) {
	if thesis == nil || thesis.EvidenceMeta == nil || thesis.EvidenceMeta.ConfidenceVector == nil {
		return
	}
	baseline := thesis.EvidenceMeta.ConfidenceVector.CompetenceConfidence
	learned := (thesis.CompetenceTrust + thesis.CompetenceConfidence) / 2
	if learned <= 0 {
		return
	}
	if learned > baseline {
		thesis.EvidenceMeta.ConfidenceVector.CompetenceConfidence = learned
		thesis.EvidenceMeta.EvidenceScore = thesis.EvidenceMeta.ConfidenceVector.Overall()
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
	if thesis == nil {
		return
	}
	if d.store != nil {
		if err := d.store.UpsertThesis(ctx, thesis); err != nil {
			d.log.Warn("persist thesis failed", "thesis_id", thesis.ID, "error", err)
		}
	}
	if err := d.graph.UpsertThesis(ctx, thesis); err != nil {
		d.log.Warn("persist thesis to graph failed", "thesis_id", thesis.ID, "error", err)
	}
}

func (d *Desk) recordCollaborationInput(ctx context.Context, thesis *model.Thesis, sig signal.Signal) {
	if thesis == nil || !isInternalSignal(sig) {
		return
	}
	if err := d.graph.RecordCollaborationInput(ctx, thesis, sig, d.ID, d.Domain); err != nil {
		d.log.Warn("persist collaboration input to graph failed", "thesis_id", thesis.ID, "signal_id", sig.ID, "error", err)
	}
}

func (d *Desk) persistPosition(ctx context.Context, pos *model.Position) {
	if pos == nil {
		return
	}
	if d.store != nil {
		if err := d.store.UpsertPosition(ctx, pos); err != nil {
			d.log.Warn("persist position failed", "position_id", pos.ID, "error", err)
		}
	}
	if err := d.graph.UpsertPosition(ctx, pos); err != nil {
		d.log.Warn("persist position to graph failed", "position_id", pos.ID, "error", err)
	}
}

func (d *Desk) recordAntiPortfolio(ctx context.Context, thesis *model.Thesis, reason string) {
	if thesis == nil {
		return
	}
	if d.store != nil {
		if err := d.store.InsertAntiPortfolio(ctx, thesis, reason); err != nil {
			d.log.Warn("persist anti-portfolio failed", "thesis_id", thesis.ID, "error", err)
		}
		if err := d.store.InsertEventLog(ctx, antiPortfolioDecisionEvent(d.ID, d.Domain, thesis, reason, d.minConviction)); err != nil {
			d.log.Warn("persist anti-portfolio decision event failed", "thesis_id", thesis.ID, "reason", reason, "error", err)
		}
	}
	if err := d.graph.RecordAntiPortfolio(ctx, thesis, reason); err != nil {
		d.log.Warn("persist anti-portfolio to graph failed", "thesis_id", thesis.ID, "error", err)
	}
}

func antiPortfolioDecisionEvent(deskID, domain string, thesis *model.Thesis, reason string, minConviction float64) store.EventLogEntry {
	stage := rejectionStage(reason)
	metadata := map[string]any{
		"stage":                 stage,
		"rejection_reason":      reason,
		"thesis_id":             thesis.ID,
		"opportunity_id":        thesis.OpportunityID,
		"desk_id":               deskID,
		"domain":                firstNonEmptyInternal(thesis.Domain, domain),
		"strategy":              thesis.Strategy,
		"symbol":                thesis.Instrument.Symbol,
		"sec_type":              thesis.Instrument.SecType,
		"direction":             string(thesis.Direction),
		"status_at_rejection":   string(thesis.Status),
		"conviction":            thesis.Conviction,
		"min_conviction":        minConviction,
		"position_size":         thesis.PositionSize,
		"entry_price":           thesis.EntryPrice,
		"target_price":          thesis.TargetPrice,
		"stop_loss":             thesis.StopLoss,
		"health":                thesis.Health,
		"evidence_count":        len(thesis.Evidence),
		"counter_args":          thesis.CounterArgs,
		"counterfactual_status": "not_evaluated",
		"counterfactual_reason": "counterfactual_pnl_not_evaluated",
		"autonomy_mode":         thesis.AutonomyMode,
		"scan_territory":        thesis.ScanTerritory,
		"execution_territory":   thesis.ExecutionTerritory,
		"competence_key":        thesis.CompetenceKey,
		"competence_trust":      thesis.CompetenceTrust,
		"competence_confidence": thesis.CompetenceConfidence,
	}
	if thesis.Prosecution != nil {
		metadata["prosecution_verdict"] = thesis.Prosecution.Verdict
		metadata["prosecution_confidence"] = thesis.Prosecution.Confidence
		metadata["prosecution_bear_args"] = thesis.Prosecution.BearArgs
		metadata["prosecution_analogues"] = thesis.Prosecution.Analogues
	}
	if thesis.CouncilVerdict != nil {
		metadata["council_approved"] = thesis.CouncilVerdict.Approved
		metadata["council_adjusted_conviction"] = thesis.CouncilVerdict.AdjustedConviction
		metadata["council_adjusted_size"] = thesis.CouncilVerdict.AdjustedSize
		metadata["council_weighted_vote_score"] = thesis.CouncilVerdict.WeightedVoteScore
		metadata["council_total_weight"] = thesis.CouncilVerdict.TotalWeight
		metadata["council_voice_count"] = len(thesis.CouncilVerdict.Voices)
		metadata["council_voices"] = thesis.CouncilVerdict.Voices
	}
	if thesis.QuantMetrics != nil {
		metadata["quant_metrics"] = thesis.QuantMetrics
	}
	if thesis.EvidenceMeta != nil {
		metadata["evidence_meta"] = thesis.EvidenceMeta
	}

	return store.EventLogEntry{
		Timestamp: time.Now().UTC(),
		EventType: "thesis_rejected",
		DeskID:    deskID,
		Severity:  rejectionSeverity(reason),
		Message:   fmt.Sprintf("thesis rejected at %s: %s", stage, reason),
		Metadata:  metadata,
	}
}

func rejectionStage(reason string) string {
	switch strings.TrimSpace(reason) {
	case "conviction_below_threshold":
		return "research"
	case "killed_by_prosecutor", "prosecutor_weakened_below_threshold":
		return "prosecutor"
	case "council_rejected":
		return "council"
	case "blocked_by_runtime_health":
		return "runtime_health"
	case "blocked_by_risk_gate":
		return "risk"
	case "kalshi_executor_unavailable", "kalshi_execution_rejected", "kalshi_order_not_filled":
		return "execution"
	default:
		return "unknown"
	}
}

func rejectionSeverity(reason string) string {
	switch strings.TrimSpace(reason) {
	case "blocked_by_runtime_health", "kalshi_executor_unavailable", "kalshi_execution_rejected":
		return "warn"
	default:
		return "info"
	}
}

func (d *Desk) recordScannerRejection(ctx context.Context, sig signal.Signal, eval scanner.Evaluation) {
	if d == nil || d.store == nil {
		return
	}
	entry := scannerRejectionEvent(d.ID, d.Domain, sig, eval)
	if err := d.store.InsertEventLog(ctx, entry); err != nil {
		d.log.Warn("persist scanner rejection event failed",
			"signal_id", sig.ID,
			"reason", eval.Reason,
			"error", err,
		)
	}
}

func scannerRejectionEvent(deskID, domain string, sig signal.Signal, eval scanner.Evaluation) store.EventLogEntry {
	reason := strings.TrimSpace(eval.Reason)
	if reason == "" {
		reason = "scanner_rejected"
	}
	return store.EventLogEntry{
		Timestamp: time.Now().UTC(),
		EventType: "scanner_rejected",
		DeskID:    deskID,
		Severity:  scannerRejectionSeverity(reason),
		Message:   fmt.Sprintf("scanner rejected signal: %s", reason),
		Metadata: map[string]any{
			"desk_id":           deskID,
			"domain":            domain,
			"signal_id":         sig.ID,
			"signal_source":     sig.Source,
			"signal_type":       string(sig.Type),
			"signal_category":   sig.Category,
			"signal_urgency":    sig.Urgency,
			"scanner_reason":    reason,
			"scanner_score":     eval.Score,
			"scanner_tradeable": eval.Tradeable,
			"signal_excerpt":    institutional.TruncateForPrompt(firstNonEmptyInternal(sig.Translated, sig.OriginalText, string(sig.Raw)), 320),
		},
	}
}

func scannerRejectionSeverity(reason string) string {
	switch strings.TrimSpace(reason) {
	case "llm_error", "parse_error", "llm_cooldown":
		return "warn"
	default:
		return "info"
	}
}

func (d *Desk) logStage(stage string, started time.Time, fields ...any) {
	duration := time.Since(started).Round(time.Millisecond)
	fields = append(fields, "stage", stage, "duration", duration.String())
	if duration >= deskSlowStageWarnAt {
		d.log.Warn("desk stage slow", fields...)
		return
	}
	d.log.Debug("desk stage complete", fields...)
}
