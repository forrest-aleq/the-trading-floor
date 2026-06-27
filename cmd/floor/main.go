package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // trading-day rollover needs America/New_York even in scratch containers

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/execution/ibkr"
	kalshiexec "github.com/hnic/trading-floor/internal/execution/kalshi"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/graphdb"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/marketcontext"
	"github.com/hnic/trading-floor/internal/marketdata"
	"github.com/hnic/trading-floor/internal/marketrefs"
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
	firm.ReloadRuntimeConfig()
	research.ReloadRuntimeConfig()
	scanner.ReloadRuntimeConfig()

	sessionID := uuid.New().String()[:8]
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("=== THE TRADING FLOOR ===", "session_id", sessionID)
	slog.Info("initializing autonomous trading system")

	runtimeMode, err := loadRuntimeMode()
	if err != nil {
		slog.Error("invalid runtime mode", "error", err)
		os.Exit(1)
	}
	slog.Info("runtime mode selected", "mode", runtimeMode)

	desks, err := activeDeskConfig()
	if err != nil {
		slog.Error("invalid desk runtime configuration", "error", err)
		os.Exit(1)
	}
	brokerExecutionRequired := desksRequireBrokerExecution(desks)
	kalshiExecutionRequired := desksRequireKalshiExecution(desks)
	slog.Info("desk runtime selected",
		"desks", len(desks),
		"broker_execution_required", brokerExecutionRequired,
		"kalshi_execution_required", kalshiExecutionRequired,
	)

	kalshiExecutor, err := kalshiexec.NewExecutorFromEnv()
	if err != nil {
		slog.Warn("Kalshi execution disabled by configuration error", "error", err)
	}
	if kalshiExecutor != nil {
		slog.Info("Kalshi execution adapter initialized", "dry_run", kalshiExecutor.IsDryRun())
	}
	var kalshiBankroll *kalshiBankrollMonitor
	if kalshiExecutionRequired {
		kalshiBankroll = newKalshiBankrollMonitor(kalshiExecutor)
		kalshiBankroll.Refresh(ctx)
		kalshiBankroll.Start(ctx)
	}

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
		defer func() {
			if err := graph.Close(ctx); err != nil {
				slog.Warn("Neo4j close failed", "error", err)
			}
		}()
		slog.Info("Neo4j connected")
	}

	// --- IBKR ---
	ibkrRuntimeEnabled := brokerExecutionRequired || readRuntimeBool("FLOOR_ENABLE_IBKR_RUNTIME", false)
	pacing := ibkr.NewPacingBudget()
	if ibkrRuntimeEnabled {
		observe.SafeGo(slog.Default().With("component", "runtime"), "ibkr pacing loop panic", func() {
			pacing.Run(ctx)
		}, "task", "ibkr_pacing")
	}

	ibkrCfg := ibkr.DefaultConfig()
	ibkrClient := ibkr.NewClient(ibkrCfg)
	if !ibkrRuntimeEnabled {
		slog.Info("IBKR startup skipped; selected desks do not use broker execution",
			"host", ibkrCfg.Host,
			"port", ibkrCfg.Port,
		)
	} else if err := ibkrClient.Connect(ctx); err != nil {
		brokerStatus := ibkrClient.ConnectionStatus()
		slog.Warn("IBKR unavailable at startup — continuing in degraded mode while reconnect loop retries",
			"error", err,
			"host", ibkrCfg.Host,
			"port", ibkrCfg.Port,
			"client_id", brokerStatus.ClientID,
			"last_connect_error", brokerStatus.LastConnectErr,
			"last_attempt_at", brokerStatus.LastAttemptAt,
		)
	} else {
		slog.Info("IBKR connected", "paper", ibkrClient.IsPaper())
	}
	defer ibkrClient.Close()

	// --- Book + Execution ---
	var orderJournal execution.OrderJournal
	if db != nil {
		orderJournal = db
	}
	execMgr := execution.NewManagerWithJournal(ibkrClient, orderJournal)
	if db != nil {
		hydrateCtx, hydrateCancel := context.WithTimeout(ctx, 10*time.Second)
		if err := execMgr.HydrateWorkingOrders(hydrateCtx); err != nil {
			slog.Warn("hydrate working orders failed", "error", err)
		}
		hydrateCancel()
	}
	bk := book.NewBook(ibkrClient, 1_000_000)
	if db != nil {
		hydrateCtx, hydrateCancel := context.WithTimeout(ctx, 10*time.Second)
		positions, err := db.ListOpenPositions(hydrateCtx, false)
		hydrateCancel()
		if err != nil {
			slog.Warn("hydrate open positions failed", "error", err)
		} else if count := bk.HydrateOpenPositions(positions); count > 0 {
			slog.Info("runtime book hydrated from store", "positions", count)
		}
	}
	if ibkrRuntimeEnabled {
		observe.SafeGo(slog.Default().With("component", "runtime"), "book reconcile loop panic", func() {
			bk.StartReconcile(ctx)
		}, "task", "book_reconcile")
	} else {
		slog.Info("broker book reconciliation disabled; selected desks do not use broker execution")
	}
	slog.Info("book and execution initialized")

	// --- Centralized Market Data ---
	marketState, err := loadMarketStateProvider(ibkrClient, ibkrClient, pacing)
	if err != nil {
		slog.Error("invalid market data provider", "error", err)
		os.Exit(1)
	}
	marketStateLabel := firstNonEmpty(marketState.Label, string(marketState.Mode))
	marketDataPollInterval := readRuntimeDuration("MARKET_DATA_POLL_INTERVAL", 30*time.Second)
	if strings.HasPrefix(marketStateLabel, "massive_free+") && marketDataPollInterval < time.Minute {
		marketDataPollInterval = time.Minute
	}
	mdMgr := marketdata.NewManager(marketState.Provider, marketState.RequestBudget, marketDataPollInterval)
	marketBootstrap := marketrefs.StartupPricingWatchlist()
	healthRequiredQuotes := marketBootstrap
	if strings.HasPrefix(marketStateLabel, "massive_free+") {
		healthRequiredQuotes = limitInstruments(marketBootstrap, readRuntimeInt("MARKET_DATA_REQUIRED_QUOTE_LIMIT", 4))
	}
	minFreshHealthQuotes := readRuntimeInt("MARKET_DATA_MIN_FRESH_QUOTES", len(healthRequiredQuotes))
	disableHealthQuoteGate := readRuntimeBool("MARKET_DATA_DISABLE_HEALTH_QUOTE_GATE", false)
	var positionWriter *positionPersistenceWriter
	if db != nil {
		positionWriter = newPositionPersistenceWriter(
			db,
			readRuntimeDuration("POSITION_PERSIST_FLUSH_INTERVAL", 2*time.Second),
			readRuntimeDuration("POSITION_PERSIST_WRITE_TIMEOUT", 15*time.Second),
		)
		observe.SafeGo(slog.Default().With("component", "runtime"), "position persistence loop panic", func() {
			positionWriter.Run(ctx)
		}, "task", "position_persist")
	}
	mdMgr.AddInstruments(marketBootstrap)
	mdMgr.Subscribe(func(prices map[string]float64) {
		bk.Mark(prices)
		if positionWriter != nil {
			positionWriter.Enqueue(bk.GetOpenPositions())
		}
	})
	startOpenPositionMarketDataSync(ctx, bk, mdMgr, readRuntimeDuration("POSITION_MARK_WATCHLIST_SYNC_INTERVAL", 15*time.Second))
	if positionWriter != nil {
		snapshotInterval := readRuntimeDuration("POSITION_PERSIST_SNAPSHOT_INTERVAL", 30*time.Second)
		observe.SafeGo(slog.Default().With("component", "runtime"), "position persistence snapshot loop panic", func() {
			ticker := time.NewTicker(snapshotInterval)
			defer ticker.Stop()
			positionWriter.Enqueue(bk.GetOpenPositions())
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					positionWriter.Enqueue(bk.GetOpenPositions())
				}
			}
		}, "task", "position_persist_snapshot", "interval", snapshotInterval.String())
	}
	if marketState.Provider != nil {
		observe.SafeGo(slog.Default().With("component", "runtime"), "market data loop panic", func() {
			mdMgr.Run(ctx)
		}, "task", "marketdata")
		slog.Info("market data manager initialized",
			"provider", firstNonEmpty(marketState.Label, string(marketState.Mode)),
			"watchlist", len(marketBootstrap),
			"poll_interval", marketDataPollInterval,
			"broker_backed", marketState.BrokerBacked,
		)
	} else {
		slog.Warn("market data manager initialized without live provider; cache-only mode",
			"provider", firstNonEmpty(marketState.Label, string(marketState.Mode)),
			"watchlist", len(marketBootstrap),
			"poll_interval", marketDataPollInterval,
		)
	}

	if err := validateRuntimeReadiness(runtimeReadiness{
		Mode:                    runtimeMode,
		DBReady:                 db != nil,
		BrokerExecutionRequired: brokerExecutionRequired,
		KalshiExecutionRequired: kalshiExecutionRequired,
		KalshiExecutionReady:    kalshiExecutor != nil,
		KalshiDryRun:            kalshiExecutor == nil || kalshiExecutor.IsDryRun(),
		BrokerConnected:         ibkrClient.IsConnected(),
		BrokerPaper:             ibkrClient.IsPaper(),
		MarketStateConfigured:   marketState.Provider != nil,
		MarketStateBrokerBacked: marketState.BrokerBacked,
		StartupPricingReady:     len(marketBootstrap) > 0,
		EarningsUniverseReady:   len(marketrefs.EarningsWatchlist()) > 0,
		RegimeDetectionEnabled:  marketrefs.RegimeDetectionEnabled(),
		RegimeDetectorReady:     marketrefs.RegimeDetectionEnabled() && marketState.Provider != nil,
		RiskTokenConfigured:     hasConfiguredRiskTokenSecret(),
	}); err != nil {
		slog.Error("runtime readiness validation failed",
			"mode", runtimeMode,
			"db_ready", db != nil,
			"broker_execution_required", brokerExecutionRequired,
			"kalshi_execution_required", kalshiExecutionRequired,
			"kalshi_execution_ready", kalshiExecutor != nil,
			"kalshi_dry_run", kalshiExecutor == nil || kalshiExecutor.IsDryRun(),
			"broker_connected", ibkrClient.IsConnected(),
			"broker_paper", ibkrClient.IsPaper(),
			"market_data_provider", firstNonEmpty(marketState.Label, string(marketState.Mode)),
			"market_data_configured", marketState.Provider != nil,
			"market_data_broker_backed", marketState.BrokerBacked,
			"startup_watchlist", len(marketBootstrap),
			"earnings_watchlist", len(marketrefs.EarningsWatchlist()),
			"regime_detection", marketrefs.RegimeDetectionEnabled(),
			"regime_detector_ready", marketrefs.RegimeDetectionEnabled() && marketState.Provider != nil,
			"risk_token_configured", hasConfiguredRiskTokenSecret(),
			"error", err,
		)
		os.Exit(1)
	}

	// --- Wire (Signal Feeds) ---
	wireMgr := wire.NewManager()
	startManualInputServer(ctx, manualInputServerConfig{
		Enabled: readRuntimeBool("MANUAL_INPUT_SERVER_ENABLED", false),
		Bind:    os.Getenv("MANUAL_INPUT_BIND"),
		Token:   os.Getenv("MANUAL_INPUT_TOKEN"),
		Publish: wireMgr.Publish,
	})

	// --- Shared Services ---
	riskGate := risk.NewGate(risk.DefaultLimits())
	execMgr.SetTokenValidator(riskGate)
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
		beliefGraph.SetLeadTimeChangeHandler(func(rel *model.SourceLeadTimeBelief) {
			if graph != nil {
				if err := graph.UpsertSourceLeadTimeBelief(context.Background(), rel); err != nil {
					slog.Warn("persist source lead-time belief to graph failed", "key", rel.Key, "error", err)
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
		beliefGraph.SetLeadTimeChangeHandler(func(rel *model.SourceLeadTimeBelief) {
			if err := graph.UpsertSourceLeadTimeBelief(context.Background(), rel); err != nil {
				slog.Warn("persist source lead-time belief to graph failed", "key", rel.Key, "error", err)
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

		leadTimeBeliefs, err := graph.LoadSourceLeadTimeBeliefs(ctx)
		if err != nil {
			slog.Warn("load source lead-time beliefs from graph failed", "error", err)
		} else {
			beliefGraph.LoadLeadTimeBeliefs(leadTimeBeliefs)
			slog.Info("source lead-time beliefs hydrated", "count", len(leadTimeBeliefs))
		}
	}
	learnWorker := memory.NewLearnWorker(beliefGraph, engramStore)
	if runtimeMode == runtimeModePaperDiscovery {
		scanner.ApplyPaperDiscoveryDefaults()
	}
	decisionThresholds := loadDecisionThresholdsForMode(runtimeMode)
	scan := scanner.NewEngine(llmRouter, decisionThresholds.ScannerMinScore)
	researchDesk := research.NewDesk(llmRouter, decisionThresholds.ResearchMinConviction)
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
		if ok {
			sig = wire.ApplyLearnedSourceReliability(sig, state.Trust, state.Confidence)
		}
		leadBelief, ok := beliefGraph.LookupLeadTime(
			sig.Source,
			firstNonEmptyRuntime(sig.Category, string(sig.Type)),
			meta.OriginalLanguage,
			meta.OriginRegion,
		)
		if !ok {
			return sig
		}
		return wire.ApplyLeadTimeBelief(sig, leadBelief.AverageHours, leadBelief.Observations, leadBelief.Score)
	})
	wireMgr.SetLeadTimeObservationHandler(func(source, category, language, region string, observedHours float64) {
		if observedHours <= 0 {
			return
		}
		beliefGraph.RecordLeadTimeObservation(belief.LeadTimeBeliefKey(source, category, language, region), observedHours)
	})
	startBeliefDecay(ctx, beliefGraph)
	slog.Info("decision services initialized",
		"runtime_mode", runtimeMode,
		"scanner_min_score", decisionThresholds.ScannerMinScore,
		"research_min_conviction", decisionThresholds.ResearchMinConviction,
		"desk_min_conviction", decisionThresholds.DeskMinConviction,
		"council_threshold", decisionThresholds.CouncilThreshold,
	)

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
	defer func() {
		if err := audit.Close(); err != nil {
			slog.Warn("audit log close failed", "error", err)
		}
	}()
	audit.Record("system_start", "", "", map[string]any{
		"session_id":     sessionID,
		"paper":          ibkrClient.IsPaper(),
		"capital":        1_000_000,
		"db_persistence": db != nil,
		"graph_brain":    graph != nil,
		"desks":          len(desks),
	})

	globalEntryControl := firm.NewManualEntryControl(firm.NormalEntryPolicy(time.Now().UTC()))
	var brokerEntryControl firm.EntryControl
	if brokerExecutionRequired && (runtimeMode != runtimeModeDev || marketState.Provider != nil) {
		defaultMaxQuoteAge := 2 * time.Minute
		if strings.HasPrefix(marketStateLabel, "massive_free+") {
			defaultMaxQuoteAge = 10 * time.Minute
		}
		healthInterval := readRuntimeDuration("RUNTIME_HEALTH_INTERVAL", 15*time.Second)
		maxBrokerSyncAge := readRuntimeDuration("RUNTIME_HEALTH_MAX_BROKER_SYNC_AGE", 2*time.Minute)
		maxQuoteAge := readRuntimeDuration("RUNTIME_HEALTH_MAX_QUOTE_AGE", defaultMaxQuoteAge)
		persistenceProbeTimeout := readRuntimeDuration("RUNTIME_HEALTH_PERSISTENCE_TIMEOUT", 2*time.Second)
		minBrokerSMA := readRuntimeFloat("RUNTIME_HEALTH_MIN_BROKER_SMA_DOLLARS", 25_000)
		minBrokerExcessLiquidity := readRuntimeFloat("RUNTIME_HEALTH_MIN_BROKER_EXCESS_LIQUIDITY_DOLLARS", 50_000)
		minBrokerBuyingPower := readRuntimeFloat("RUNTIME_HEALTH_MIN_BROKER_BUYING_POWER_DOLLARS", 0)
		brokerAckFailureThreshold := readRuntimeInt("RUNTIME_HEALTH_BROKER_ACK_FAILURE_THRESHOLD", 3)
		brokerAckFailureWindow := readRuntimeDuration("RUNTIME_HEALTH_BROKER_ACK_FAILURE_WINDOW", 2*time.Minute)
		brokerAckFailureCooldown := readRuntimeDuration("RUNTIME_HEALTH_BROKER_ACK_FAILURE_COOLDOWN", 5*time.Minute)
		health := newRuntimeHealthSupervisor(runtimeHealthConfig{
			Broker:                    ibkrClient,
			BrokerStatus:              ibkrClient,
			BrokerSync:                bk,
			MarketFreshness:           mdMgr,
			PersistenceProbe:          db,
			RequiredQuotes:            healthRequiredQuotes,
			MinFreshQuotes:            minFreshHealthQuotes,
			DisableQuoteGate:          disableHealthQuoteGate,
			Interval:                  healthInterval,
			MaxBrokerSyncAge:          maxBrokerSyncAge,
			MaxQuoteAge:               maxQuoteAge,
			PersistenceProbeTimeout:   persistenceProbeTimeout,
			MinBrokerSMA:              minBrokerSMA,
			MinBrokerExcessLiquidity:  minBrokerExcessLiquidity,
			MinBrokerBuyingPower:      minBrokerBuyingPower,
			BrokerAckFailureThreshold: brokerAckFailureThreshold,
			BrokerAckFailureWindow:    brokerAckFailureWindow,
			BrokerAckFailureCooldown:  brokerAckFailureCooldown,
			OnPolicyChange: func(policy firm.EntryPolicy, details map[string]any) {
				audit.Record("runtime_entry_policy", "", "", map[string]any{
					"mode":          policy.Mode,
					"allow_entries": policy.AllowEntries,
					"reason":        policy.Reason,
					"updated_at":    policy.UpdatedAt,
					"details":       details,
				})
			},
		})
		execMgr.SetBrokerOrderFailureObserver(health)
		brokerEntryControl = health
		health.EvaluateNow(time.Now().UTC())
		observe.SafeGo(slog.Default().With("component", "runtime"), "runtime health loop panic", func() {
			health.Run(ctx)
		}, "task", "runtime_health")
		slog.Info("runtime health supervisor initialized",
			"required_quotes", len(healthRequiredQuotes),
			"min_fresh_quotes", normalizeMinFreshQuotes(minFreshHealthQuotes, len(healthRequiredQuotes)),
			"market_data_quote_gate_disabled", disableHealthQuoteGate,
			"interval", healthInterval,
			"max_broker_sync_age", maxBrokerSyncAge,
			"max_quote_age", maxQuoteAge,
			"persistence_timeout", persistenceProbeTimeout,
			"min_broker_sma", minBrokerSMA,
			"min_broker_excess_liquidity", minBrokerExcessLiquidity,
			"min_broker_buying_power", minBrokerBuyingPower,
			"broker_ack_failure_threshold", brokerAckFailureThreshold,
			"broker_ack_failure_window", brokerAckFailureWindow,
			"broker_ack_failure_cooldown", brokerAckFailureCooldown,
		)
	} else if !brokerExecutionRequired {
		slog.Info("runtime health supervisor disabled; selected desks do not use broker execution")
	} else {
		slog.Warn("runtime health supervisor disabled in dev mode without live market data provider")
	}

	feedCount := registerDefaultFeeds(wireMgr, mdMgr, desks)

	// --- Floor + Desks ---
	floor := firm.NewFloor(wireMgr, sessionID)
	floor.SetStore(db)
	floor.SetGraph(graph)
	desksByID := map[string]*firm.Desk{}

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
			Kalshi:        kalshiExecutor,
			Book:          bk,
			Beliefs:       beliefGraph,
			LearnWorker:   learnWorker,
			Engrams:       engramStore,
			Store:         db,
			Graph:         graph,
			OnTrade:       floor.RecordTrade,
			Watchlist:     mdMgr.AddInstruments,
			PublishSignal: wireMgr.Publish,
			EntryControl:  entryControlForDesk(d, globalEntryControl, brokerEntryControl),

			MinConviction:    decisionThresholds.DeskMinConviction,
			CouncilThreshold: decisionThresholds.CouncilThreshold,
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

	// --- Working Order Monitor ---
	workingOrderPollInterval := readRuntimeDuration("EXECUTION_WORKING_ORDER_POLL_INTERVAL", 15*time.Second)
	stalePaperOrderAge := readRuntimeDuration("EXECUTION_STALE_PAPER_ORDER_AGE", 0)
	observe.SafeGo(slog.Default().With("component", "runtime"), "working order loop panic", func() {
		if workingOrderPollInterval <= 0 {
			return
		}

		runWorkingOrderPass := func() {
			refreshCtx, refreshCancel := context.WithTimeout(ctx, 10*time.Second)
			updates := execMgr.RefreshWorkingOrders(refreshCtx)
			refreshCancel()

			for _, update := range updates {
				if handleBrokerRecoveryWorkingOrderUpdate(ctx, bk, db, update) {
					continue
				}
				desk := desksByID[update.Snapshot.DeskID]
				if desk == nil {
					slog.Warn("working order update for unknown desk",
						"desk_id", update.Snapshot.DeskID,
						"order_id", update.Snapshot.OrderID,
						"state", update.Snapshot.State,
					)
					continue
				}
				switch update.Snapshot.State {
				case execution.OrderStatePartiallyFilled:
					if update.Fill == nil {
						slog.Warn("partial fill update missing cumulative fill payload",
							"desk_id", update.Snapshot.DeskID,
							"order_id", update.Snapshot.OrderID,
							"broker_order_id", update.Snapshot.BrokerOrderID,
						)
						continue
					}
					if _, err := desk.RecordExecutionFill(ctx, update.Fill); err != nil {
						slog.Warn("reconcile broker partial fill failed",
							"desk_id", update.Snapshot.DeskID,
							"order_id", update.Snapshot.OrderID,
							"error", err,
						)
						continue
					}
					slog.Info("broker partial fill applied to book",
						"desk_id", update.Snapshot.DeskID,
						"order_id", update.Snapshot.OrderID,
						"broker_order_id", update.Snapshot.BrokerOrderID,
						"filled_quantity", update.Snapshot.FilledQuantity,
						"remaining_quantity", update.Snapshot.RemainingQuantity,
						"fill_ratio", update.Snapshot.ExecutionQuality.FillRatio,
						"implementation_shortfall_bps", update.Snapshot.ExecutionQuality.ImplementationShortfallBps,
					)
				case execution.OrderStateFilled:
					if _, err := desk.RecordExecutionFill(ctx, update.Fill); err != nil {
						slog.Warn("reconcile broker fill failed",
							"desk_id", update.Snapshot.DeskID,
							"order_id", update.Snapshot.OrderID,
							"error", err,
						)
					}
				case execution.OrderStateCancelled, execution.OrderStateFailed:
					desk.ResolvePendingExecution(ctx, update.Snapshot.OrderID, update.Snapshot.State, update.Snapshot.BrokerStatus)
				}
			}

			if !execMgr.IsPaper() || stalePaperOrderAge <= 0 {
				return
			}
			now := time.Now().UTC()
			for _, working := range execMgr.WorkingOrders() {
				if strings.EqualFold(working.BrokerStatus, "cancel_requested") {
					continue
				}
				if working.FilledQuantity > 0 {
					continue
				}
				if working.SubmittedAt.IsZero() || now.Sub(working.SubmittedAt) < stalePaperOrderAge {
					continue
				}
				cancelCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := execMgr.CancelWorkingOrder(cancelCtx, working.OrderID)
				cancel()
				if err != nil {
					slog.Warn("cancel stale paper working order failed",
						"order_id", working.OrderID,
						"broker_order_id", working.BrokerOrderID,
						"error", err,
					)
					continue
				}
				slog.Warn("stale paper working order cancel requested",
					"order_id", working.OrderID,
					"broker_order_id", working.BrokerOrderID,
					"age", now.Sub(working.SubmittedAt),
					"symbol", working.DisplaySymbol,
				)
			}
		}

		runWorkingOrderPass()
		ticker := time.NewTicker(workingOrderPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runWorkingOrderPass()
			}
		}
	}, "task", "working_orders")

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
				if pending, ok := pendingFillError(err); ok {
					slog.Info("closing order already working; deferring book close until fill",
						"position_id", pos.ID,
						"broker_order_id", pending.OrderID,
						"broker_status", pending.Status,
					)
					return
				}
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
	monitor.SetStaleMarkMaxAge(readRuntimeDuration("MONITOR_STALE_MARK_MAX_AGE", 2*time.Minute))
	monitor.SetLifecycleHandler(func(pos *model.Position, alert model.LifecycleAlert) {
		audit.Record("position_lifecycle", pos.DeskID, pos.ThesisID, map[string]any{
			"kind":       alert.Kind,
			"severity":   alert.Severity,
			"message":    alert.Message,
			"instrument": alert.Instrument,
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
	ceo.SetEntryControl(globalEntryControl)
	observe.SafeGo(slog.Default().With("component", "runtime"), "ceo loop panic", func() {
		ceo.Run(ctx)
	}, "task", "ceo")

	// --- Regime Detector ---
	if marketrefs.RegimeDetectionEnabled() {
		if marketState.Provider == nil {
			slog.Warn("regime detector disabled; no live market data provider configured")
		} else {
			regimeDetector := regime.NewDetector(marketState.Provider, func(old, newRegime model.Regime) {
				ceo.ForceRegimeShift(newRegime)
				audit.Record("regime_shift", "", "", map[string]any{
					"old": old.Key(),
					"new": newRegime.Key(),
				})
			})
			observe.SafeGo(slog.Default().With("component", "runtime"), "regime detector panic", func() {
				regimeDetector.Run(ctx)
			}, "task", "regime_detector")
		}
	} else {
		slog.Info("regime detector disabled by market refs policy")
	}

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
	startRuntimeHeartbeat(ctx, floor, bk, execMgr, brokerEntryControl, disableHealthQuoteGate, kalshiBankroll)

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

func normalizeClosedAt(pos *model.Position) time.Time {
	if pos != nil && pos.ClosedAt != nil {
		return pos.ClosedAt.UTC()
	}
	return time.Now().UTC()
}

const brokerRecoveryDeskID = "broker-recovery"

func pendingFillError(err error) (*execution.PendingFillError, bool) {
	var pending *execution.PendingFillError
	if errors.As(err, &pending) {
		return pending, true
	}
	return nil, false
}

func handleBrokerRecoveryWorkingOrderUpdate(ctx context.Context, bk *book.Book, db *store.DB, update execution.OrderUpdate) bool {
	if strings.TrimSpace(update.Snapshot.DeskID) != brokerRecoveryDeskID {
		return false
	}

	switch update.Snapshot.State {
	case execution.OrderStateWorking:
		return true
	case execution.OrderStatePartiallyFilled:
		if update.Snapshot.FilledQuantity > 0 {
			slog.Warn("broker recovery close order partially filled; awaiting broker position reconciliation",
				"order_id", update.Snapshot.OrderID,
				"broker_order_id", update.Snapshot.BrokerOrderID,
				"filled_quantity", update.Snapshot.FilledQuantity,
				"remaining_quantity", update.Snapshot.RemainingQuantity,
				"status", update.Snapshot.BrokerStatus,
			)
		}
		return true
	case execution.OrderStateFilled:
		positionID, ok := brokerRecoveryPositionIDFromCloseOrder(update.Snapshot.OrderID)
		if !ok {
			slog.Warn("broker recovery close order has unexpected id",
				"order_id", update.Snapshot.OrderID,
				"broker_order_id", update.Snapshot.BrokerOrderID,
			)
			return true
		}
		exitPrice := brokerRecoveryExitPrice(update)
		if exitPrice <= 0 {
			slog.Warn("broker recovery close fill missing price",
				"position_id", positionID,
				"order_id", update.Snapshot.OrderID,
				"broker_order_id", update.Snapshot.BrokerOrderID,
			)
			return true
		}
		outcome, err := bk.ClosePosition(positionID, exitPrice, "broker_recovery_exit_filled")
		if err != nil {
			slog.Warn("broker recovery close fill failed",
				"position_id", positionID,
				"order_id", update.Snapshot.OrderID,
				"broker_order_id", update.Snapshot.BrokerOrderID,
				"error", err,
			)
			return true
		}
		if outcome == nil {
			return true
		}
		if db != nil {
			if pos, ok := bk.GetPosition(positionID); ok && pos != nil && pos.ClosedAt != nil {
				if err := db.UpdatePositionClose(ctx, pos.ID, outcome.RealizedPnL, exitPrice, *pos.ClosedAt); err != nil {
					slog.Warn("persist broker recovery position close failed", "position_id", pos.ID, "error", err)
				}
			}
		}
		slog.Info("broker recovery close fill applied to book",
			"position_id", positionID,
			"order_id", update.Snapshot.OrderID,
			"broker_order_id", update.Snapshot.BrokerOrderID,
			"exit_price", exitPrice,
			"pnl", outcome.RealizedPnL,
		)
		return true
	case execution.OrderStateCancelled, execution.OrderStateFailed:
		if update.Snapshot.FilledQuantity > 0 {
			slog.Warn("broker recovery close order terminal after partial fill; manual reconciliation required",
				"order_id", update.Snapshot.OrderID,
				"broker_order_id", update.Snapshot.BrokerOrderID,
				"state", update.Snapshot.State,
				"status", update.Snapshot.BrokerStatus,
				"filled_quantity", update.Snapshot.FilledQuantity,
				"remaining_quantity", update.Snapshot.RemainingQuantity,
				"error", update.Snapshot.LastError,
			)
			return true
		}
		slog.Warn("broker recovery close order terminal without fill",
			"order_id", update.Snapshot.OrderID,
			"broker_order_id", update.Snapshot.BrokerOrderID,
			"state", update.Snapshot.State,
			"status", update.Snapshot.BrokerStatus,
			"error", update.Snapshot.LastError,
		)
		return true
	default:
		return true
	}
}

func brokerRecoveryPositionIDFromCloseOrder(orderID string) (string, bool) {
	orderID = strings.TrimSpace(orderID)
	if !strings.HasSuffix(orderID, "-close") {
		return "", false
	}
	positionID := strings.TrimSuffix(orderID, "-close")
	return positionID, positionID != ""
}

func brokerRecoveryExitPrice(update execution.OrderUpdate) float64 {
	if update.Fill != nil && update.Fill.AvgPrice > 0 {
		return update.Fill.AvgPrice
	}
	if update.Snapshot.AvgFillPrice > 0 {
		return update.Snapshot.AvgFillPrice
	}
	if update.Snapshot.LastFillPrice > 0 {
		return update.Snapshot.LastFillPrice
	}
	return 0
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

func startRuntimeHeartbeat(ctx context.Context, floor *firm.Floor, bk *book.Book, execMgr *execution.Manager, brokerEntryControl firm.EntryControl, marketDataQuoteGateDisabled bool, kalshiBankroll *kalshiBankrollMonitor) {
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
				brokerSync := bk.BrokerSyncStatus()
				tokenUsage := llm.TokenUsageStats()
				workingOrders := 0
				workingOrdersByStatus := map[string]int{}
				if execMgr != nil {
					for _, working := range execMgr.WorkingOrders() {
						workingOrders++
						status := strings.TrimSpace(working.BrokerStatus)
						if status == "" {
							status = string(working.State)
						}
						workingOrdersByStatus[status]++
					}
				}

				signalDelta := stats.SignalsProcessed - prevSignals
				tradeDelta := stats.TradesExecuted - prevTrades
				receivedDelta := stats.WireStats.TotalReceived - prevReceived

				fields := []any{
					"workers", stats.Workers,
					"default_workers", stats.DefaultWorkers,
					"prediction_workers", stats.PredictionWorkers,
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
					"default_task_queue_depth", stats.DefaultTaskQueueDepth,
					"default_task_queue_capacity", stats.DefaultTaskQueueCap,
					"prediction_task_queue_depth", stats.PredictionTaskQueueDepth,
					"prediction_task_queue_capacity", stats.PredictionTaskQueueCap,
					"overflow_depth", stats.OverflowDepth,
					"overflow_capacity", stats.OverflowCap,
					"default_overflow_depth", stats.DefaultOverflowDepth,
					"default_overflow_capacity", stats.DefaultOverflowCap,
					"prediction_overflow_depth", stats.PredictionOverflowDepth,
					"prediction_overflow_capacity", stats.PredictionOverflowCap,
					"wire_received", stats.WireStats.TotalReceived,
					"wire_received_delta", receivedDelta,
					"wire_deduped", stats.WireStats.TotalDeduped,
					"wire_corroborated", stats.WireStats.TotalCorroborated,
					"wire_overflow_pending", stats.WireStats.PendingOverflow,
					"wire_dropped", stats.WireStats.TotalDropped,
					"open_positions", snapshot.OpenPositions,
					"total_trades", snapshot.TotalTrades,
					"working_orders", workingOrders,
					"working_orders_by_status", workingOrdersByStatus,
					"nav", snapshot.NAV,
					"cash", snapshot.Cash,
					"gross_exposure", snapshot.GrossExposure,
					"net_exposure", snapshot.NetExposure,
					"daily_pnl", snapshot.DailyPnL,
					"daily_pnl_available", snapshot.DailyPnLAvailable,
					"daily_pnl_source", snapshot.DailyPnLSource,
					"monthly_pnl", snapshot.MonthlyPnL,
					"broker_sync_connected", brokerSync.Connected,
					"broker_sync_nav", brokerSync.NAV,
					"broker_sync_cash", brokerSync.Cash,
					"broker_sync_buying_power", brokerSync.BuyingPower,
					"broker_sync_equity_with_loan_value", brokerSync.EquityWithLoanValue,
					"broker_sync_gross_position_value", brokerSync.GrossPositionValue,
					"broker_sync_reg_t_equity", brokerSync.RegTEquity,
					"broker_sync_reg_t_margin", brokerSync.RegTMargin,
					"broker_sync_sma", brokerSync.SMA,
					"broker_sync_init_margin_req", brokerSync.InitMarginReq,
					"broker_sync_maint_margin_req", brokerSync.MaintMarginReq,
					"broker_sync_available_funds", brokerSync.AvailableFunds,
					"broker_sync_excess_liquidity", brokerSync.ExcessLiquidity,
					"broker_sync_daily_pnl", brokerSync.DailyPnL,
					"broker_sync_daily_pnl_available", brokerSync.DailyPnLAvailable,
					"broker_sync_daily_pnl_source", brokerSync.DailyPnLSource,
					"broker_sync_gross_exposure", brokerSync.GrossExposure,
					"broker_sync_net_exposure", brokerSync.NetExposure,
					"broker_sync_open_positions", brokerSync.OpenPositions,
					"broker_sync_last_synced", brokerSync.LastSynced,
					"broker_sync_last_account_synced", brokerSync.LastAccountSynced,
					"broker_sync_last_pnl_synced", brokerSync.LastPnLSynced,
					"broker_sync_last_positions_synced", brokerSync.LastPositionsSynced,
					"broker_sync_last_failure", brokerSync.LastFailure,
					"broker_sync_last_error", brokerSync.LastError,
					"broker_sync_consecutive_failures", brokerSync.ConsecutiveFailures,
					"received_by_source", stats.WireStats.ReceivedBySource,
					"llm_attempts", tokenUsage.Attempts,
					"llm_calls", tokenUsage.Calls,
					"llm_errors", tokenUsage.Errors,
					"llm_input_tokens", tokenUsage.InputTokens,
					"llm_output_tokens", tokenUsage.OutputTokens,
					"llm_total_tokens", tokenUsage.TotalTokens,
					"llm_estimated_input_tokens_failed", tokenUsage.EstimatedInputTokens,
					"llm_tokens_by_desk", tokenUsage.DeskTokens,
					"llm_calls_by_desk", tokenUsage.DeskCalls,
					"llm_attempts_by_desk", tokenUsage.DeskAttempts,
					"llm_errors_by_desk", tokenUsage.DeskErrors,
					"llm_tokens_by_stage", tokenUsage.StageTokens,
					"llm_attempts_by_stage", tokenUsage.StageAttempts,
					"llm_errors_by_stage", tokenUsage.StageErrors,
					"llm_tokens_by_model", tokenUsage.ModelTokens,
					"llm_attempts_by_model", tokenUsage.ModelAttempts,
					"llm_errors_by_model", tokenUsage.ModelErrors,
					"llm_last_model", tokenUsage.LastModel,
				}
				if brokerEntryControl != nil {
					policy := brokerEntryControl.CurrentEntryPolicy()
					fields = append(fields,
						"broker_entry_mode", policy.Mode,
						"broker_entry_allow_entries", policy.AllowEntries,
						"broker_entry_reason", policy.Reason,
						"broker_entry_updated_at", policy.UpdatedAt,
						"market_data_quote_gate_disabled", marketDataQuoteGateDisabled,
					)
					if ackTelemetry, ok := brokerEntryControl.(interface {
						BrokerAckFailureTelemetry(time.Time) map[string]any
					}); ok {
						for key, value := range ackTelemetry.BrokerAckFailureTelemetry(time.Now().UTC()) {
							fields = append(fields, key, value)
						}
					}
				}
				if !tokenUsage.LastUpdated.IsZero() {
					fields = append(fields, "llm_last_usage_age", time.Since(tokenUsage.LastUpdated).Round(time.Second).String())
				}
				if kalshiBankroll != nil {
					fields = append(fields, kalshiBankroll.HeartbeatFields()...)
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
						"default_task_queue_depth", stats.DefaultTaskQueueDepth,
						"prediction_task_queue_depth", stats.PredictionTaskQueueDepth,
						"overflow_depth", stats.OverflowDepth,
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

type decisionThresholdConfig struct {
	ScannerMinScore       float64
	ResearchMinConviction float64
	DeskMinConviction     float64
	CouncilThreshold      float64
}

func loadDecisionThresholds() decisionThresholdConfig {
	return loadDecisionThresholdsForMode(runtimeModeDev)
}

func loadDecisionThresholdsForMode(mode runtimeMode) decisionThresholdConfig {
	scannerDefault := 70.0
	researchDefault := 0.65
	if mode == runtimeModePaperDiscovery {
		scannerDefault = 55
		researchDefault = 0.50
	}
	researchMin := readRuntimeFloatRange("RESEARCH_MIN_CONVICTION", researchDefault, 0.01, 1.0)
	return decisionThresholdConfig{
		ScannerMinScore:       readRuntimeFloatRange("SCANNER_MIN_SCORE", scannerDefault, 1, 100),
		ResearchMinConviction: researchMin,
		DeskMinConviction:     readRuntimeFloatRange("DESK_MIN_CONVICTION", researchMin, 0.01, 1.0),
		CouncilThreshold:      readRuntimeFloatRange("DESK_COUNCIL_THRESHOLD", 0.02, 0.0001, 1.0),
	}
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

func readRuntimeFloatRange(name string, fallback, min, max float64) float64 {
	value := readRuntimeFloat(name, fallback)
	if value < min || value > max {
		return fallback
	}
	return value
}

func readRuntimeInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func readRuntimeBool(name string, fallback bool) bool {
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

func limitInstruments(instruments []model.Instrument, limit int) []model.Instrument {
	if limit <= 0 || limit >= len(instruments) {
		return instruments
	}
	return append([]model.Instrument(nil), instruments[:limit]...)
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

func activeDeskConfig() ([]deskDef, error) {
	desks := fullDeskConfig()
	if readRuntimeBool("FLOOR_ENABLE_KALSHI_DESKS", false) {
		desks = append(desks, kalshiDeskConfig()...)
	}
	if readRuntimeBool("FLOOR_ENABLE_PREDICTION_MARKET_DESK", false) {
		desks = append(desks, predictionMarketDeskConfig()...)
	}
	if allowlist := strings.TrimSpace(os.Getenv("FLOOR_ENABLED_DESKS")); allowlist != "" {
		selected, err := filterDeskConfig(desks, allowlist)
		if err != nil {
			return nil, err
		}
		desks = selected
	}

	if rawLimit := strings.TrimSpace(os.Getenv("FLOOR_DESK_LIMIT")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit <= 0 {
			return nil, fmt.Errorf("FLOOR_DESK_LIMIT must be a positive integer")
		}
		if limit < len(desks) {
			desks = desks[:limit]
		}
	}

	if len(desks) == 0 {
		return nil, fmt.Errorf("desk runtime configuration selected zero desks")
	}
	return desks, nil
}

func deskUsesKalshiExecution(d deskDef) bool {
	return strings.EqualFold(strings.TrimSpace(d.domain), "prediction_market")
}

func deskUsesBrokerExecution(d deskDef) bool {
	return !deskUsesKalshiExecution(d)
}

func desksRequireKalshiExecution(desks []deskDef) bool {
	for _, desk := range desks {
		if deskUsesKalshiExecution(desk) {
			return true
		}
	}
	return false
}

func desksRequireBrokerExecution(desks []deskDef) bool {
	for _, desk := range desks {
		if deskUsesBrokerExecution(desk) {
			return true
		}
	}
	return false
}

func entryControlForDesk(d deskDef, globalControl firm.EntryControl, brokerControl firm.EntryControl) firm.EntryControl {
	if deskUsesKalshiExecution(d) {
		return globalControl
	}
	return firm.NewCombinedEntryControl(globalControl, brokerControl)
}

func filterDeskConfig(desks []deskDef, rawAllowlist string) ([]deskDef, error) {
	byID := make(map[string]deskDef, len(desks))
	for _, desk := range desks {
		byID[desk.id] = desk
	}

	seen := map[string]struct{}{}
	selected := make([]deskDef, 0, len(desks))
	for _, part := range strings.Split(rawAllowlist, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		desk, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("FLOOR_ENABLED_DESKS references unknown desk %q", id)
		}
		selected = append(selected, desk)
		seen[id] = struct{}{}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("FLOOR_ENABLED_DESKS did not select any desks")
	}
	return selected, nil
}

func registerDefaultFeeds(wireMgr *wire.Manager, marketClient feeds.MarketDataClient, desks []deskDef) int {
	marketWatchlist := feeds.DefaultWatchlist()
	earningsWatchlist := feeds.DefaultEarningsWatchlist()
	registered := 0
	kalshiOnly := desksRequireKalshiExecution(desks) && !desksRequireBrokerExecution(desks)

	feedSet := []wire.Feed{}
	if !kalshiOnly {
		feedSet = append(feedSet,
			feeds.NewNewsFeed(nil),
			feeds.NewEDGARFeed(),
			feeds.NewSocialFeed(),
			feeds.NewMacroFeed(os.Getenv("FRED_API_KEY")),
			feeds.NewTelegramFeed(nil),
			feeds.NewEarningsFeed(os.Getenv("FMP_API_KEY"), earningsWatchlist),
			feeds.NewAlternativeFeed(nil),
		)
	}
	if kalshiClient := feeds.NewKalshiClientFromEnv(); kalshiClient != nil {
		kalshiFeed := feeds.NewKalshiFeed(kalshiClient)
		kalshiFeed.SetSportsAvailabilityProvider(feeds.NewESPNSportsAvailabilityProviderFromEnv())
		feedSet = append(feedSet, kalshiFeed)
	}
	if marketClient != nil && !kalshiOnly {
		feedSet = append(feedSet, feeds.NewMarketFeed(marketClient, marketWatchlist))
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

func predictionMarketDeskConfig() []deskDef {
	return []deskDef{
		{"pred-markets-a", "prediction_market", "A", 10_000},
	}
}

func kalshiDeskConfig() []deskDef {
	ids := []string{
		"kalshi-rates-a",
		"kalshi-macro-a",
		"kalshi-elections-a",
		"kalshi-weather-a",
		"kalshi-sports-a",
		"kalshi-crypto-a",
		"kalshi-tech-a",
		"kalshi-culture-a",
		"kalshi-marketstructure-a",
	}
	capital := readRuntimeFloat("KALSHI_DESK_CAPITAL_DOLLARS", 0)
	desks := make([]deskDef, 0, len(ids))
	for _, id := range ids {
		desks = append(desks, deskDef{id: id, domain: "prediction_market", group: "A", capital: capital})
	}
	return desks
}

type kalshiBankrollMonitor struct {
	mu       sync.RWMutex
	executor *kalshiexec.Executor
	latest   kalshiBankrollSnapshot
}

type kalshiBankrollSnapshot struct {
	Available             bool
	Source                string
	Reason                string
	AccountEquity         float64
	HasAccountEquity      bool
	AvailableCash         float64
	HasAvailableCash      bool
	PortfolioValue        float64
	HasPortfolioValue     bool
	MaxOrderRisk          float64
	EffectiveOrderRisk    float64
	HasEffectiveOrderRisk bool
	RiskPctEquity         float64
	UpdatedAt             time.Time
}

func newKalshiBankrollMonitor(executor *kalshiexec.Executor) *kalshiBankrollMonitor {
	return &kalshiBankrollMonitor{executor: executor}
}

func (m *kalshiBankrollMonitor) Start(ctx context.Context) {
	if m == nil {
		return
	}
	interval := readRuntimeDuration("KALSHI_BANKROLL_REFRESH_INTERVAL", 2*time.Minute)
	log := slog.Default().With("component", "kalshi-bankroll")
	observe.SafeGo(log, "Kalshi bankroll monitor panic", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Refresh(ctx)
			}
		}
	}, "interval", interval.String())
}

func (m *kalshiBankrollMonitor) Refresh(ctx context.Context) {
	if m == nil {
		return
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	snapshot := readKalshiBankrollSnapshot(refreshCtx, m.executor)
	m.mu.Lock()
	m.latest = snapshot
	m.mu.Unlock()
	logKalshiBankroll(snapshot)
}

func (m *kalshiBankrollMonitor) HeartbeatFields() []any {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	snapshot := m.latest
	m.mu.RUnlock()
	return snapshot.HeartbeatFields()
}

func readKalshiBankrollSnapshot(ctx context.Context, executor *kalshiexec.Executor) kalshiBankrollSnapshot {
	snapshot := kalshiBankrollSnapshot{
		MaxOrderRisk:  readRuntimeFloat("KALSHI_MAX_ORDER_DOLLARS", 0),
		RiskPctEquity: readRuntimeFloat("KALSHI_RISK_PCT_EQUITY", 0),
		UpdatedAt:     time.Now().UTC(),
	}
	if raw, ok := os.LookupEnv("KALSHI_ACCOUNT_EQUITY_DOLLARS"); ok && strings.TrimSpace(raw) != "" {
		configured, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || configured < 0 {
			snapshot.Source = "unavailable"
			snapshot.Reason = "invalid_configured_account_equity"
			return snapshot
		}
		snapshot.Available = true
		snapshot.Source = "configured_account_equity"
		snapshot.AccountEquity = configured
		snapshot.HasAccountEquity = true
		return snapshot
	}
	if executor == nil {
		snapshot.Source = "unavailable"
		snapshot.Reason = "executor_unavailable"
		return snapshot
	}
	balance, err := executor.AccountBalance(ctx)
	if err != nil {
		snapshot.Source = "unavailable"
		snapshot.Reason = err.Error()
		return snapshot
	}

	equityCents := balance.Balance + balance.PortfolioValue
	snapshot.Available = true
	snapshot.Source = "kalshi_api"
	snapshot.AccountEquity = float64(equityCents) / 100.0
	snapshot.HasAccountEquity = true
	snapshot.AvailableCash = float64(balance.Balance) / 100.0
	snapshot.HasAvailableCash = true
	snapshot.PortfolioValue = float64(balance.PortfolioValue) / 100.0
	snapshot.HasPortfolioValue = true
	snapshot.EffectiveOrderRisk = float64(executor.EffectiveMaxOrderCents(ctx)) / 100.0
	snapshot.HasEffectiveOrderRisk = true
	return snapshot
}

func (s kalshiBankrollSnapshot) HeartbeatFields() []any {
	fields := []any{
		"kalshi_bankroll_available", s.Available,
		"kalshi_bankroll_source", s.Source,
		"kalshi_max_order_risk", s.MaxOrderRisk,
		"kalshi_risk_pct_equity", s.RiskPctEquity,
		"kalshi_bankroll_updated_at", s.UpdatedAt,
	}
	if s.Reason != "" {
		fields = append(fields, "kalshi_bankroll_reason", s.Reason)
	}
	if s.HasAccountEquity {
		fields = append(fields, "kalshi_account_equity", s.AccountEquity)
	}
	if s.HasAvailableCash {
		fields = append(fields, "kalshi_available_cash", s.AvailableCash)
	}
	if s.HasPortfolioValue {
		fields = append(fields, "kalshi_portfolio_value", s.PortfolioValue)
	}
	if s.HasEffectiveOrderRisk {
		fields = append(fields, "kalshi_effective_order_risk", s.EffectiveOrderRisk)
	}
	return fields
}

func logKalshiBankroll(snapshot kalshiBankrollSnapshot) {
	fields := []any{
		"available", snapshot.Available,
		"source", snapshot.Source,
		"max_order_risk", snapshot.MaxOrderRisk,
		"risk_pct_equity", snapshot.RiskPctEquity,
	}
	if snapshot.Reason != "" {
		fields = append(fields, "reason", snapshot.Reason)
	}
	if snapshot.HasAccountEquity {
		fields = append(fields, "equity", snapshot.AccountEquity)
	}
	if snapshot.HasAvailableCash {
		fields = append(fields, "available_cash", snapshot.AvailableCash)
	}
	if snapshot.HasPortfolioValue {
		fields = append(fields, "portfolio_value", snapshot.PortfolioValue)
	}
	if snapshot.HasEffectiveOrderRisk {
		fields = append(fields, "effective_max_order_risk", snapshot.EffectiveOrderRisk)
	}
	if snapshot.Available {
		slog.Info("Kalshi bankroll status", fields...)
		return
	}
	slog.Warn("Kalshi bankroll status", fields...)
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
