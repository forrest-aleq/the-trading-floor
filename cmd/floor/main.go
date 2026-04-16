package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/graphdb"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/marketcontext"
	"github.com/hnic/trading-floor/internal/marketdata"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/observe"
	"github.com/hnic/trading-floor/internal/orderflow"
	"github.com/hnic/trading-floor/internal/quant"
	"github.com/hnic/trading-floor/internal/regime"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/store"
	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/internal/wire/feeds"
	"github.com/hnic/trading-floor/pkg/model"
	sigpkg "github.com/hnic/trading-floor/pkg/signal"
)

func main() {
	_ = godotenv.Load()

	sessionID := uuid.New().String()[:8]
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("=== THE TRADING FLOOR ===", "session_id", sessionID)
	slog.Info("initializing autonomous trading system")

	// --- LLM ---
	llmRouter := llm.DefaultRouter()
	slog.Info("LLM router initialized",
		"speed_model", os.Getenv("LLM_MODEL_SPEED"),
		"analysis_model", os.Getenv("LLM_MODEL_ANALYSIS"),
		"critical_model", os.Getenv("LLM_MODEL_CRITICAL"),
	)

	// --- PostgreSQL ---
	db, err := store.NewDB(ctx)
	if err != nil {
		slog.Warn("PostgreSQL not available — running without persistence", "error", err)
	} else {
		defer db.Close()
		slog.Info("PostgreSQL connected")
	}

	// --- Neo4j ---
	graph, err := graphdb.NewFromEnv(ctx)
	if err != nil {
		slog.Warn("Neo4j not available — running without graph brain", "error", err)
	} else if graph != nil {
		defer graph.Close(ctx)
		slog.Info("Neo4j connected")
	}

	// --- IBKR ---
	pacing := ibkr.NewPacingBudget()
	observe.SafeGo(slog.Default().With("component", "runtime"), "ibkr pacing loop panic", func() {
		pacing.Run(ctx)
	}, "task", "ibkr_pacing")

	ibkrCfg := ibkr.DefaultConfig()
	ibkrClient := ibkr.NewClient(ibkrCfg)
	if err := ibkrClient.Connect(ctx); err != nil {
		slog.Warn("IBKR unavailable at startup — continuing in degraded mode while reconnect loop retries",
			"error", err,
			"host", ibkrCfg.Host,
			"port", ibkrCfg.Port,
		)
	} else {
		slog.Info("IBKR connected", "paper", ibkrClient.IsPaper())
	}
	defer ibkrClient.Close()

	// --- Book + Execution ---
	execMgr := execution.NewManager(ibkrClient)
	bk := book.NewBook(ibkrClient, 1_000_000)
	observe.SafeGo(slog.Default().With("component", "runtime"), "book reconcile loop panic", func() {
		bk.StartReconcile(ctx)
	}, "task", "book_reconcile")
	slog.Info("book and execution initialized")

	// --- Centralized Market Data ---
	mdMgr := marketdata.NewManager(ibkrClient, pacing, 0)
	marketBootstrap := feeds.DefaultWatchlist()
	mdMgr.AddInstruments(marketBootstrap)
	mdMgr.Subscribe(func(prices map[string]float64) {
		bk.Mark(prices)
		if db != nil {
			for _, pos := range bk.GetOpenPositions() {
				if err := db.UpsertPosition(ctx, pos); err != nil {
					slog.Warn("persist mark-to-market failed", "position_id", pos.ID, "error", err)
				}
			}
		}
	})
	observe.SafeGo(slog.Default().With("component", "runtime"), "market data loop panic", func() {
		mdMgr.Run(ctx)
	}, "task", "marketdata")
	slog.Info("market data manager initialized", "watchlist", len(marketBootstrap))

	// --- Wire (Signal Feeds) ---
	wireMgr := wire.NewManager()

	// --- Shared Services ---
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	engramStore := memory.NewEngramStore()
	slog.Info("shared memory services initialized")
	if db != nil {
		slog.Info("hydrating persisted competence state")
		states, err := db.LoadCompetenceStates(ctx)
		if err != nil {
			slog.Warn("load competence states failed", "error", err)
		} else {
			beliefGraph.Load(states)
			for _, state := range states {
				if graph != nil {
					if err := graph.UpsertCompetenceState(ctx, state); err != nil {
						slog.Warn("persist hydrated competence state to graph failed", "key", state.Key, "error", err)
					}
				}
			}
			slog.Info("competence states hydrated", "count", len(states))
		}

		slog.Info("hydrating persisted engrams")
		engramRecords, err := db.LoadEngrams(ctx)
		if err != nil {
			slog.Warn("load engrams failed", "error", err)
		} else {
			engramStore.Load(engramsFromRecords(engramRecords))
			slog.Info("engrams hydrated", "count", len(engramRecords))
		}

		beliefGraph.SetChangeHandler(func(state *model.CompetenceState) {
			if err := db.UpsertCompetenceState(context.Background(), state); err != nil {
				slog.Warn("persist competence state failed", "key", state.Key, "error", err)
			}
			if graph != nil {
				if err := graph.UpsertCompetenceState(context.Background(), state); err != nil {
					slog.Warn("persist competence state to graph failed", "key", state.Key, "error", err)
				}
			}
		})
		beliefGraph.SetPeerChangeHandler(func(rel *model.DeskRelationshipBelief) {
			if graph != nil {
				if err := graph.UpsertDeskRelationshipBelief(context.Background(), rel); err != nil {
					slog.Warn("persist desk relationship belief to graph failed", "key", rel.Key, "error", err)
				}
			}
		})
		beliefGraph.SetSourceChangeHandler(func(rel *model.SourceReliabilityBelief) {
			if graph != nil {
				if err := graph.UpsertSourceReliabilityBelief(context.Background(), rel); err != nil {
					slog.Warn("persist source reliability belief to graph failed", "key", rel.Key, "error", err)
				}
			}
		})
		engramStore.SetChangeHandler(func(engram *memory.Engram) {
			if err := db.UpsertEngram(context.Background(), engramRecordFromMemory(engram)); err != nil {
				slog.Warn("persist engram failed", "id", engram.ID, "error", err)
			}
		})
	} else if graph != nil {
		beliefGraph.SetChangeHandler(func(state *model.CompetenceState) {
			if err := graph.UpsertCompetenceState(context.Background(), state); err != nil {
				slog.Warn("persist competence state to graph failed", "key", state.Key, "error", err)
			}
		})
		beliefGraph.SetPeerChangeHandler(func(rel *model.DeskRelationshipBelief) {
			if err := graph.UpsertDeskRelationshipBelief(context.Background(), rel); err != nil {
				slog.Warn("persist desk relationship belief to graph failed", "key", rel.Key, "error", err)
			}
		})
		beliefGraph.SetSourceChangeHandler(func(rel *model.SourceReliabilityBelief) {
			if err := graph.UpsertSourceReliabilityBelief(context.Background(), rel); err != nil {
				slog.Warn("persist source reliability belief to graph failed", "key", rel.Key, "error", err)
			}
		})
	}
	if graph != nil {
		peerBeliefs, err := graph.LoadDeskRelationshipBeliefs(ctx)
		if err != nil {
			slog.Warn("load desk relationship beliefs from graph failed", "error", err)
		} else {
			beliefGraph.LoadPeerBeliefs(peerBeliefs)
			slog.Info("desk relationship beliefs hydrated", "count", len(peerBeliefs))
		}

		sourceBeliefs, err := graph.LoadSourceReliabilityBeliefs(ctx)
		if err != nil {
			slog.Warn("load source reliability beliefs from graph failed", "error", err)
		} else {
			beliefGraph.LoadSourceBeliefs(sourceBeliefs)
			slog.Info("source reliability beliefs hydrated", "count", len(sourceBeliefs))
		}
	}
	learnWorker := memory.NewLearnWorker(beliefGraph, engramStore)
	scan := scanner.NewEngine(llmRouter, 70)
	researchDesk := research.NewDesk(llmRouter, 0.65)
	researchDesk.SetMarketContextService(marketcontext.NewService(mdMgr))
	quantService := quant.NewService()
	prosecutor := research.NewProsecutor(llmRouter)
	council := research.NewCouncil(llmRouter)
	if graph != nil {
		council.SetVoiceTelemetryProvider(graph)
	}
	wireMgr.SetSignalEnricher(func(sig sigpkg.Signal) sigpkg.Signal {
		meta := sig.EvidenceMeta
		if meta == nil {
			return sig
		}
		state, ok := beliefGraph.LookupSource(
			meta.SourceOwnerGroup,
			meta.SourceDomain,
			firstNonEmptyRuntime(sig.Category, string(sig.Type)),
			meta.OriginalLanguage,
			meta.OriginRegion,
		)
		if !ok {
			return sig
		}
		return wire.ApplyLearnedSourceReliability(sig, state.Trust, state.Confidence)
	})
	startBeliefDecay(ctx, beliefGraph)
	slog.Info("decision services initialized")

	// --- Audit Log ---
	auditPath := os.Getenv("AUDIT_LOG_PATH")
	if auditPath == "" {
		auditPath = filepath.Join("var", "audit", "audit.jsonl")
	}

	slog.Info("initializing audit log", "path", auditPath)
	audit, err := observe.NewAuditLog(auditPath)
	if err != nil {
		slog.Error("audit log init failed", "error", err)
		os.Exit(1)
	}
	defer audit.Close()
	audit.Record("system_start", "", "", map[string]any{
		"session_id":     sessionID,
		"paper":          ibkrClient.IsPaper(),
		"capital":        1_000_000,
		"db_persistence": db != nil,
		"graph_brain":    graph != nil,
		"desks":          40,
	})

	feedCount := registerDefaultFeeds(wireMgr, ibkrClient)

	// --- Floor + Desks ---
	floor := firm.NewFloor(wireMgr, sessionID)
	floor.SetStore(db)
	floor.SetGraph(graph)
	desksByID := map[string]*firm.Desk{}

	// 40 desks: 20 Group A (full MARS beliefs) + 20 Group B (control, no belief updates)
	// 8 domains × ~5 desks each, split A/B
	desks := fullDeskConfig()

	for _, d := range desks {
		desk := firm.NewDesk(firm.DeskConfig{
			ID:            d.id,
			Domain:        d.domain,
			ABGroup:       d.group,
			Capital:       d.capital,
			LLM:           llmRouter,
			Scanner:       scan,
			Research:      researchDesk,
			Quant:         quantService,
			Prosecutor:    prosecutor,
			Council:       council,
			RiskGate:      riskGate,
			Execution:     execMgr,
			Book:          bk,
			Beliefs:       beliefGraph,
			LearnWorker:   learnWorker,
			Engrams:       engramStore,
			Store:         db,
			Graph:         graph,
			OnTrade:       floor.RecordTrade,
			Watchlist:     mdMgr.AddInstruments,
			PublishSignal: wireMgr.Publish,
		})
		desksByID[d.id] = desk
		floor.AddDesk(desk)
		if graph != nil {
			if err := graph.UpsertDesk(ctx, d.id, d.domain, d.group); err != nil {
				slog.Warn("persist desk to graph failed", "desk_id", d.id, "error", err)
			}
		}
	}

	// --- Thesis Lookup ---
	thesisLookup := func(thesisID string) (*model.Thesis, bool) {
		for _, desk := range desksByID {
			if thesis, ok := desk.GetThesis(thesisID); ok {
				return thesis, true
			}
		}
		if db == nil {
			return nil, false
		}
		thesis, err := db.GetThesis(ctx, thesisID)
		if err != nil {
			slog.Warn("load thesis failed", "thesis_id", thesisID, "error", err)
			return nil, false
		}
		return thesis, thesis != nil
	}

	// --- Position Monitor ---
	orderCompiler := orderflow.NewCompiler()
	monitor := book.NewMonitor(bk, thesisLookup, func(pos *model.Position, exitPrice float64, reason string) {
		closePrice := exitPrice
		if !pos.Shadow {
			exitOrder, err := orderCompiler.CompileExit(pos)
			if err != nil {
				slog.Error("failed to compile closing order", "position_id", pos.ID, "error", err)
				return
			}
			executionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			fill, err := execMgr.SubmitExit(executionCtx, *exitOrder)
			cancel()
			if err != nil {
				slog.Error("failed to submit closing order", "position_id", pos.ID, "error", err)
				return
			}
			if fill != nil && fill.AvgPrice > 0 {
				closePrice = fill.AvgPrice
			}
		}

		outcome, err := bk.ClosePosition(pos.ID, closePrice, reason)
		if err != nil {
			slog.Error("failed to close position", "position_id", pos.ID, "error", err)
			return
		}
		if outcome == nil {
			return
		}

		if db != nil && pos.ClosedAt != nil {
			if err := db.UpdatePositionClose(ctx, pos.ID, outcome.RealizedPnL, exitPrice, *pos.ClosedAt); err != nil {
				slog.Warn("persist position close failed", "position_id", pos.ID, "error", err)
			}
		}

		desk := desksByID[pos.DeskID]
		var thesis *model.Thesis
		if desk != nil {
			if loaded, ok := desk.GetThesis(pos.ThesisID); ok {
				thesis = loaded
				desk.ProcessOutcome(ctx, thesis, outcome)
			} else if loaded, ok := thesisLookup(pos.ThesisID); ok {
				thesis = loaded
				desk.ProcessOutcome(ctx, thesis, outcome)
			}
		}
		if graph != nil {
			if err := graph.RecordOutcome(ctx, thesis, pos, outcome, normalizeClosedAt(pos), reason); err != nil {
				slog.Warn("persist outcome to graph failed", "position_id", pos.ID, "error", err)
			}
		}

		audit.Record("position_closed", pos.DeskID, pos.ThesisID, map[string]any{
			"pnl":    outcome.RealizedPnL,
			"reason": reason,
			"price":  closePrice,
		})
	})
	observe.SafeGo(slog.Default().With("component", "runtime"), "position monitor panic", func() {
		monitor.Run(ctx)
	}, "task", "position_monitor")

	// --- CEO Referee ---
	allDesks := make([]*firm.Desk, 0, len(desksByID))
	for _, d := range desksByID {
		allDesks = append(allDesks, d)
	}
	ceo := firm.NewCEO(bk, beliefGraph, floor)
	ceo.SetDesks(allDesks)
	observe.SafeGo(slog.Default().With("component", "runtime"), "ceo loop panic", func() {
		ceo.Run(ctx)
	}, "task", "ceo")

	// --- Regime Detector ---
	regimeDetector := regime.NewDetector(ibkrClient, func(old, newRegime model.Regime) {
		ceo.ForceRegimeShift(newRegime)
		audit.Record("regime_shift", "", "", map[string]any{
			"old": old.Key(),
			"new": newRegime.Key(),
		})
	})
	observe.SafeGo(slog.Default().With("component", "runtime"), "regime detector panic", func() {
		regimeDetector.Run(ctx)
	}, "task", "regime_detector")

	groupA, groupB := 0, 0
	for _, d := range desks {
		if d.group == "A" {
			groupA++
		} else {
			groupB++
		}
	}

	slog.Info("firm initialized",
		"session_id", sessionID,
		"desks", len(desks),
		"group_a", groupA,
		"group_b", groupB,
		"feeds", feedCount,
	)

	slog.Info("trading floor is LIVE — processing signals")
	startRuntimeHeartbeat(ctx, floor, bk)

	if err := floor.Run(ctx); err != nil {
		if err == context.Canceled {
			slog.Info("trading floor stopped by user")
		} else {
			slog.Error("trading floor error", "error", err)
		}
	}

	stats := floor.Stats()
	slog.Info("final stats",
		"signals_processed", stats.SignalsProcessed,
		"trades_executed", stats.TradesExecuted,
		"wire_received", stats.WireStats.TotalReceived,
	)

	fmt.Println("trading-floor: shutdown complete")
}

func parseLogLevel(raw string) slog.Level {
	switch raw {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func heartbeatInterval() time.Duration {
	raw := os.Getenv("RUNTIME_HEARTBEAT_INTERVAL")
	if raw == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 30 * time.Second
	}
	return d
}

func oppositeDirection(direction model.TradeDirection) model.TradeDirection {
	if direction == model.Short {
		return model.Long
	}
	return model.Short
}

func normalizeClosedAt(pos *model.Position) time.Time {
	if pos != nil && pos.ClosedAt != nil {
		return pos.ClosedAt.UTC()
	}
	return time.Now().UTC()
}

func startBeliefDecay(ctx context.Context, graph *belief.Graph) {
	if graph == nil {
		return
	}

	interval := readRuntimeDuration("BELIEF_DECAY_INTERVAL", 24*time.Hour)
	decayPct := readRuntimeFloat("BELIEF_DECAY_PCT", 1.0)
	if decayPct <= 0 {
		return
	}

	log := slog.Default().With("component", "belief_decay")
	observe.SafeGo(log, "belief decay loop panic", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				graph.DecayAll(decayPct)
				log.Info("belief decay applied", "interval", interval, "decay_pct", decayPct)
			}
		}
	}, "interval", interval.String(), "decay_pct", decayPct)
}

func startRuntimeHeartbeat(ctx context.Context, floor *firm.Floor, bk *book.Book) {
	if floor == nil || bk == nil {
		return
	}

	interval := heartbeatInterval()
	log := slog.Default().With("component", "heartbeat")
	observe.SafeGo(log, "runtime heartbeat panic", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var prevSignals int64
		var prevTrades int64
		var prevReceived int64

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := floor.Stats()
				snapshot := bk.Snapshot()

				signalDelta := stats.SignalsProcessed - prevSignals
				tradeDelta := stats.TradesExecuted - prevTrades
				receivedDelta := stats.WireStats.TotalReceived - prevReceived

				fields := []any{
					"workers", stats.Workers,
					"signals_processed", stats.SignalsProcessed,
					"signals_delta", signalDelta,
					"trades_executed", stats.TradesExecuted,
					"trades_delta", tradeDelta,
					"tasks_enqueued", stats.TasksEnqueued,
					"tasks_started", stats.TasksStarted,
					"tasks_completed", stats.TasksCompleted,
					"tasks_dropped", stats.TasksDropped,
					"tasks_skipped", stats.TasksSkipped,
					"active_tasks", stats.ActiveTasks,
					"task_queue_depth", stats.TaskQueueDepth,
					"task_queue_capacity", stats.TaskQueueCap,
					"wire_received", stats.WireStats.TotalReceived,
					"wire_received_delta", receivedDelta,
					"wire_deduped", stats.WireStats.TotalDeduped,
					"wire_corroborated", stats.WireStats.TotalCorroborated,
					"wire_overflow_pending", stats.WireStats.PendingOverflow,
					"wire_dropped", stats.WireStats.TotalDropped,
					"open_positions", snapshot.OpenPositions,
					"total_trades", snapshot.TotalTrades,
					"nav", snapshot.NAV,
					"cash", snapshot.Cash,
					"gross_exposure", snapshot.GrossExposure,
					"net_exposure", snapshot.NetExposure,
					"daily_pnl", snapshot.DailyPnL,
					"monthly_pnl", snapshot.MonthlyPnL,
					"received_by_source", stats.WireStats.ReceivedBySource,
				}
				if !stats.WireStats.LastSignalAt.IsZero() {
					fields = append(fields,
						"last_signal_id", stats.WireStats.LastSignalID,
						"last_signal_source", stats.WireStats.LastSignalSource,
						"last_signal_age", time.Since(stats.WireStats.LastSignalAt).Round(time.Second).String(),
					)
				}
				log.Info("runtime heartbeat", fields...)

				if stats.WireStats.TotalReceived == 0 || (signalDelta == 0 && stats.TaskQueueDepth == 0 && stats.ActiveTasks == 0) {
					log.Warn("signal ingress is idle",
						"signals_processed", stats.SignalsProcessed,
						"wire_received", stats.WireStats.TotalReceived,
						"last_signal_source", stats.WireStats.LastSignalSource,
					)
				}
				if stats.TaskQueueDepth > 0 && stats.TasksCompleted == 0 {
					log.Warn("desk task queue is backlogged",
						"task_queue_depth", stats.TaskQueueDepth,
						"active_tasks", stats.ActiveTasks,
						"tasks_enqueued", stats.TasksEnqueued,
						"tasks_completed", stats.TasksCompleted,
					)
				}

				prevSignals = stats.SignalsProcessed
				prevTrades = stats.TradesExecuted
				prevReceived = stats.WireStats.TotalReceived
			}
		}
	})
}

func readRuntimeDuration(name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func readRuntimeFloat(name string, fallback float64) float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func firstNonEmptyRuntime(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type deskDef struct {
	id      string
	domain  string
	group   string
	capital float64
}

func registerDefaultFeeds(wireMgr *wire.Manager, marketClient feeds.MarketDataClient) int {
	marketWatchlist := feeds.DefaultWatchlist()
	earningsWatchlist := feeds.DefaultEarningsWatchlist()
	registered := 0

	feedSet := []wire.Feed{
		feeds.NewNewsFeed(nil),
		feeds.NewMarketFeed(marketClient, marketWatchlist),
		feeds.NewEDGARFeed(),
		feeds.NewSocialFeed(),
		feeds.NewMacroFeed(os.Getenv("FRED_API_KEY")),
		feeds.NewTelegramFeed(nil),
		feeds.NewEarningsFeed(os.Getenv("FMP_API_KEY"), earningsWatchlist),
		feeds.NewAlternativeFeed(nil),
	}

	for _, feed := range feedSet {
		wireMgr.RegisterFeed(feed)
		registered++
	}

	return registered
}

// fullDeskConfig returns the 40-desk configuration from DESIGN.md.
// 8 domains × 5 desks each, split into 20 Group A + 20 Group B.
func fullDeskConfig() []deskDef {
	return []deskDef{
		// Domain 1: Geopolitical (5 desks)
		{"geo-cascade-a", "geopolitical", "A", 25_000},     // Supply-chain cascade
		{"geo-event-a", "geopolitical", "A", 25_000},       // Political event-driven
		{"geo-secondorder-a", "geopolitical", "A", 25_000}, // Second-order effects
		{"geo-cascade-b", "geopolitical", "B", 25_000},
		{"geo-event-b", "geopolitical", "B", 25_000},

		// Domain 2: Macro-Economic (5 desks)
		{"macro-rates-a", "macro", "A", 25_000},      // Rate-sensitive
		{"macro-crossasset-a", "macro", "A", 25_000}, // Cross-asset macro
		{"macro-inflation-a", "macro", "A", 25_000},  // Inflation/deflation
		{"macro-rates-b", "macro", "B", 25_000},
		{"macro-crossasset-b", "macro", "B", 25_000},

		// Domain 3: Corporate (5 desks)
		{"corp-earnings-a", "corporate", "A", 25_000}, // Earnings event
		{"corp-filings-a", "corporate", "A", 25_000},  // Filing anomaly (EDGAR)
		{"corp-mna-a", "corporate", "A", 25_000},      // M&A / special sits
		{"corp-earnings-b", "corporate", "B", 25_000},
		{"corp-filings-b", "corporate", "B", 25_000},

		// Domain 4: Flows & Sentiment (5 desks)
		{"flow-options-a", "flows", "A", 25_000},    // Options flow anomaly
		{"flow-contrarian-a", "flows", "A", 25_000}, // Sentiment extreme contrarian
		{"flow-squeeze-a", "flows", "A", 25_000},    // Gamma/positioning squeeze
		{"flow-options-b", "flows", "B", 25_000},
		{"flow-contrarian-b", "flows", "B", 25_000},

		// Domain 5: Tail Risk (5 desks) — smaller capital, loses most months
		{"tail-geo-a", "tail", "A", 15_000},       // Geopolitical tail
		{"tail-financial-a", "tail", "A", 15_000}, // Financial system tail
		{"tail-structure-b", "tail", "B", 15_000}, // Market structure tail
		{"tail-geo-b", "tail", "B", 15_000},
		{"tail-financial-b", "tail", "B", 15_000},

		// Domain 6: Volatility (5 desks)
		{"vol-premium-a", "volatility", "A", 25_000},       // Variance risk premium
		{"vol-event-a", "volatility", "A", 25_000},         // Vol event trading
		{"vol-termstructure-b", "volatility", "B", 25_000}, // Term structure/calendar
		{"vol-premium-b", "volatility", "B", 25_000},
		{"vol-event-b", "volatility", "B", 25_000},

		// Domain 7: Sector Specialist (5 desks)
		{"sector-tech-a", "sector", "A", 25_000},    // Tech mega-cap
		{"sector-biotech-a", "sector", "A", 25_000}, // Biotech/FDA catalyst
		{"sector-energy-b", "sector", "B", 25_000},  // Energy
		{"sector-tech-b", "sector", "B", 25_000},
		{"sector-biotech-b", "sector", "B", 25_000},

		// Domain 8: Systematic (5 desks)
		{"sys-momentum-a", "systematic", "A", 25_000}, // Momentum/trend following
		{"sys-meanrev-a", "systematic", "A", 25_000},  // Mean reversion
		{"sys-statarb-b", "systematic", "B", 25_000},  // Statistical arbitrage
		{"sys-momentum-b", "systematic", "B", 25_000},
		{"sys-meanrev-b", "systematic", "B", 25_000},
	}
}

func engramsFromRecords(records []*store.EngramRecord) []*memory.Engram {
	engrams := make([]*memory.Engram, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		engrams = append(engrams, &memory.Engram{
			ID:             record.ID,
			IntentKey:      record.IntentKey,
			ContextPattern: record.ContextPattern,
			Capability:     record.Capability,
			DeskID:         record.DeskID,
			Layer:          record.Layer,
			SuccessCount:   record.SuccessCount,
			FailureCount:   record.FailureCount,
			AvgReturn:      record.AvgReturn,
			Sharpe:         record.Sharpe,
			RegimeTags:     append([]string(nil), record.RegimeTags...),
			CreatedAt:      record.CreatedAt,
			UpdatedAt:      record.UpdatedAt,
		})
	}
	return engrams
}

func engramRecordFromMemory(engram *memory.Engram) *store.EngramRecord {
	if engram == nil {
		return nil
	}
	return &store.EngramRecord{
		ID:             engram.ID,
		IntentKey:      engram.IntentKey,
		ContextPattern: engram.ContextPattern,
		Capability:     engram.Capability,
		DeskID:         engram.DeskID,
		Layer:          engram.Layer,
		SuccessCount:   engram.SuccessCount,
		FailureCount:   engram.FailureCount,
		AvgReturn:      engram.AvgReturn,
		Sharpe:         engram.Sharpe,
		RegimeTags:     append([]string(nil), engram.RegimeTags...),
		CreatedAt:      engram.CreatedAt,
		UpdatedAt:      engram.UpdatedAt,
	}
}
