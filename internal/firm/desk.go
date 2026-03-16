package firm

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/graphdb"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
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
	prosecutor  *research.Prosecutor
	council     *research.Council
	riskGate    *risk.Gate
	execution   *execution.Manager
	book        *book.Book
	beliefs     *belief.Graph
	learnWorker *memory.LearnWorker
	engrams     *memory.EngramStore
	store       *store.DB
	graph       *graphdb.Client
	onTrade     func()
	watchlist   func([]model.Instrument)

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
	Graph       *graphdb.Client
	OnTrade     func()
	Watchlist   func([]model.Instrument)

	MinConviction    float64
	CouncilThreshold float64
}

const (
	deskScannerTimeout    = 20 * time.Second
	deskResearchTimeout   = 45 * time.Second
	deskProsecutionTimout = 35 * time.Second
	deskCouncilTimeout    = 45 * time.Second
	deskExecutionTimeout  = 30 * time.Second
	deskSlowStageWarnAt   = 10 * time.Second
)

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
		graph:            cfg.Graph,
		onTrade:          cfg.OnTrade,
		watchlist:        cfg.Watchlist,
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
	if err := d.graph.RecordSignalSeen(ctx, sig.ID, d.ID, d.Domain, time.Now().UTC()); err != nil {
		d.log.Warn("graph signal seen failed", "signal_id", sig.ID, "error", err)
	}

	span := trace.FromContext(ctx).WithStage("scanner")
	ctx = trace.IntoContext(ctx, span)
	scanTerritory := d.assessScanTerritory()

	stageStart := time.Now()
	scanCtx, scanCancel := context.WithTimeout(ctx, deskScannerTimeout)
	opp, ok := d.scanner.Evaluate(scanCtx, sig, d.Domain)
	scanCancel()
	d.logStage("scanner", stageStart,
		"signal_id", sig.ID,
		"tradeable", ok,
		"scan_territory", scanTerritory.Status,
	)
	if !ok {
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
	thesis, err := d.research.Investigate(researchCtx, opp, d.ID)
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

	d.maybeSpawnSubTeam(ctx, thesis)

	// Engram lookup: boost conviction if we have a cached winning play for this pattern
	if d.ABGroup == "A" && d.engrams != nil {
		intentKey := thesis.Strategy + "_" + thesis.ExecutionCapability()
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
	d.normalizePositionSize(thesis)

	order := model.Order{
		ID:          thesis.ID,
		ThesisID:    thesis.ID,
		DeskID:      d.ID,
		Structure:   thesis.Structure,
		Instrument:  thesis.PrimaryInstrument(),
		Legs:        append([]model.TradeLeg(nil), thesis.Legs...),
		Direction:   thesis.Direction,
		Quantity:    thesis.PositionSize,
		OrderType:   model.OrderLimit,
		LimitPrice:  thesis.EntryPrice,
		StopPrice:   thesis.StopLoss,
		TimeInForce: "DAY",
		Notional:    thesis.GrossEntryNotional(thesis.PositionSize),
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
	if d.watchlist != nil {
		d.watchlist(thesis.ExecutionInstruments())
	}

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
			d.log.Error("execution failed", "thesis_id", thesis.ID, "error", err)
			return
		}

		span = span.WithStage("book")
		ctx = trace.IntoContext(ctx, span)
		pos = d.book.OpenPosition(fill, thesis)
		thesis.Status = model.ThesisActive
	}
	d.rememberThesis(thesis)
	d.persistThesis(ctx, thesis)
	d.persistPosition(ctx, pos)

	if d.onTrade != nil && autonomy.Mode != model.Restricted {
		d.onTrade()
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
	unitNotional := thesis.GrossEntryNotional(1)
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
		return
	}
	if decision.ScanTerritory.Exact != nil {
		thesis.CompetenceTrust = decision.ScanTerritory.Exact.Trust
		thesis.CompetenceConfidence = decision.ScanTerritory.Exact.Confidence
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
	}
	if err := d.graph.RecordAntiPortfolio(ctx, thesis, reason); err != nil {
		d.log.Warn("persist anti-portfolio to graph failed", "thesis_id", thesis.ID, "error", err)
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
