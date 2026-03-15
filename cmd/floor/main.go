package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/marketdata"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/observe"
	"github.com/hnic/trading-floor/internal/regime"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/store"
	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/internal/wire/feeds"
	"github.com/hnic/trading-floor/pkg/model"
)

func main() {
	_ = godotenv.Load()

	sessionID := uuid.New().String()[:8]

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
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

	// --- IBKR ---
	pacing := ibkr.NewPacingBudget()
	go pacing.Run(ctx)

	ibkrCfg := ibkr.DefaultConfig()
	ibkrClient := ibkr.NewClient(ibkrCfg)
	if err := ibkrClient.Connect(ctx); err != nil {
		slog.Error("IBKR connection failed", "error", err)
		os.Exit(1)
	}
	defer ibkrClient.Close()
	slog.Info("IBKR connected", "paper", ibkrClient.IsPaper())

	// --- Book + Execution ---
	execMgr := execution.NewManager(ibkrClient)
	bk := book.NewBook(ibkrClient, 1_000_000)
	go bk.StartReconcile(ctx)

	// --- Centralized Market Data ---
	mdMgr := marketdata.NewManager(ibkrClient, pacing, 0)
	mdMgr.AddInstruments(feeds.DefaultWatchlist())
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
	go mdMgr.Run(ctx)

	// --- Shared Services ---
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	engramStore := memory.NewEngramStore()
	learnWorker := memory.NewLearnWorker(beliefGraph, engramStore)
	scan := scanner.NewEngine(llmRouter, 70)
	researchDesk := research.NewDesk(llmRouter, 0.65)
	prosecutor := research.NewProsecutor(llmRouter)
	council := research.NewCouncil(llmRouter)

	// --- Audit Log ---
	audit, err := observe.NewAuditLog("audit.jsonl")
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
		"desks":          40,
	})

	// --- Wire (Signal Feeds) ---
	wireMgr := wire.NewManager()
	feedCount := registerDefaultFeeds(wireMgr, ibkrClient)

	// --- Floor + Desks ---
	floor := firm.NewFloor(wireMgr, sessionID)
	desksByID := map[string]*firm.Desk{}

	// 40 desks: 20 Group A (full MARS beliefs) + 20 Group B (control, no belief updates)
	// 8 domains × ~5 desks each, split A/B
	desks := fullDeskConfig()

	for _, d := range desks {
		desk := firm.NewDesk(firm.DeskConfig{
			ID:          d.id,
			Domain:      d.domain,
			ABGroup:     d.group,
			Capital:     d.capital,
			LLM:         llmRouter,
			Scanner:     scan,
			Research:    researchDesk,
			Prosecutor:  prosecutor,
			Council:     council,
			RiskGate:    riskGate,
			Execution:   execMgr,
			Book:        bk,
			Beliefs:     beliefGraph,
			LearnWorker: learnWorker,
			Engrams:     engramStore,
			Store:       db,
			OnTrade:     floor.RecordTrade,
		})
		desksByID[d.id] = desk
		floor.AddDesk(desk)
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
	monitor := book.NewMonitor(bk, thesisLookup, func(pos *model.Position, exitPrice float64, reason string) {
		outcome, err := bk.ClosePosition(pos.ID, exitPrice, reason)
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
		if desk != nil {
			if thesis, ok := desk.GetThesis(pos.ThesisID); ok {
				desk.ProcessOutcome(ctx, thesis, outcome)
			} else if thesis, ok := thesisLookup(pos.ThesisID); ok {
				desk.ProcessOutcome(ctx, thesis, outcome)
			}
		}

		audit.Record("position_closed", pos.DeskID, pos.ThesisID, map[string]any{
			"pnl":    outcome.RealizedPnL,
			"reason": reason,
			"price":  exitPrice,
		})
	})
	go monitor.Run(ctx)

	// --- CEO Referee ---
	allDesks := make([]*firm.Desk, 0, len(desksByID))
	for _, d := range desksByID {
		allDesks = append(allDesks, d)
	}
	ceo := firm.NewCEO(bk, beliefGraph, floor)
	ceo.SetDesks(allDesks)
	go ceo.Run(ctx)

	// --- Regime Detector ---
	regimeDetector := regime.NewDetector(ibkrClient, func(old, newRegime model.Regime) {
		ceo.ForceRegimeShift(newRegime)
		audit.Record("regime_shift", "", "", map[string]any{
			"old": old.Key(),
			"new": newRegime.Key(),
		})
	})
	go regimeDetector.Run(ctx)

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

type deskDef struct {
	id      string
	domain  string
	group   string
	capital float64
}

func registerDefaultFeeds(wireMgr *wire.Manager, marketClient feeds.MarketDataClient) int {
	watchlist := feeds.DefaultWatchlist()
	registered := 0

	feedSet := []wire.Feed{
		feeds.NewNewsFeed(nil),
		feeds.NewMarketFeed(marketClient, watchlist),
		feeds.NewEDGARFeed(),
		feeds.NewSocialFeed(),
		feeds.NewMacroFeed(os.Getenv("FRED_API_KEY")),
		feeds.NewTelegramFeed(nil),
		feeds.NewEarningsFeed(os.Getenv("EARNINGS_API_KEY"), watchlist),
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
