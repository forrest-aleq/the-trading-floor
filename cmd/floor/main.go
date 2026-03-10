package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/observe"
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

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("=== THE TRADING FLOOR ===")
	slog.Info("initializing autonomous trading system")

	llmRouter := llm.DefaultRouter()
	slog.Info("LLM router initialized",
		"speed_model", os.Getenv("LLM_MODEL_SPEED"),
		"analysis_model", os.Getenv("LLM_MODEL_ANALYSIS"),
		"critical_model", os.Getenv("LLM_MODEL_CRITICAL"),
	)

	db, err := store.NewDB(ctx)
	if err != nil {
		slog.Warn("PostgreSQL not available — running without persistence", "error", err)
	} else {
		defer db.Close()
		slog.Info("PostgreSQL connected")
	}

	ibkrCfg := ibkr.DefaultConfig()
	ibkrClient := ibkr.NewClient(ibkrCfg)
	if err := ibkrClient.Connect(ctx); err != nil {
		slog.Error("IBKR connection failed", "error", err)
		os.Exit(1)
	}
	defer ibkrClient.Close()
	slog.Info("IBKR connected", "paper", ibkrClient.IsPaper())

	execMgr := execution.NewManager(ibkrClient)
	bk := book.NewBook(ibkrClient, 1_000_000)
	go bk.StartReconcile(ctx)

	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	learnWorker := memory.NewLearnWorker(beliefGraph)
	scan := scanner.NewEngine(llmRouter, 40)
	researchDesk := research.NewDesk(llmRouter, 0.65)
	prosecutor := research.NewProsecutor(llmRouter)

	audit, err := observe.NewAuditLog("audit.jsonl")
	if err != nil {
		slog.Error("audit log init failed", "error", err)
		os.Exit(1)
	}
	defer audit.Close()
	audit.Record("system_start", "", "", map[string]any{
		"paper":          ibkrClient.IsPaper(),
		"capital":        1_000_000,
		"db_persistence": db != nil,
	})

	wireMgr := wire.NewManager()
	wireMgr.RegisterFeed(feeds.NewNewsFeed(nil))
	wireMgr.RegisterFeed(feeds.NewMarketFeed(ibkrClient, feeds.DefaultWatchlist()))

	floor := firm.NewFloor(wireMgr)
	desksByID := map[string]*firm.Desk{}

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

	go markToMarketLoop(ctx, bk, ibkrClient, db)

	domains := []struct {
		id     string
		domain string
		group  string
	}{
		{"geo-a1", "geopolitical", "A"},
		{"macro-a1", "macro", "A"},
		{"corp-a1", "corporate", "A"},
		{"vol-a1", "volatility", "A"},
		{"sector-a1", "sector", "A"},
		{"geo-b1", "geopolitical", "B"},
		{"macro-b1", "macro", "B"},
		{"corp-b1", "corporate", "B"},
		{"vol-b1", "volatility", "B"},
		{"sector-b1", "sector", "B"},
	}

	for _, d := range domains {
		desk := firm.NewDesk(firm.DeskConfig{
			ID:          d.id,
			Domain:      d.domain,
			ABGroup:     d.group,
			Capital:     25_000,
			Scanner:     scan,
			Research:    researchDesk,
			Prosecutor:  prosecutor,
			RiskGate:    riskGate,
			Execution:   execMgr,
			Book:        bk,
			Beliefs:     beliefGraph,
			LearnWorker: learnWorker,
			Store:       db,
			OnTrade:     floor.RecordTrade,
		})
		desksByID[d.id] = desk
		floor.AddDesk(desk)
	}

	slog.Info("firm initialized",
		"desks", len(domains),
		"group_a", 5,
		"group_b", 5,
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

func markToMarketLoop(ctx context.Context, bk *book.Book, client interface {
	ReqMarketData(context.Context, model.Instrument) (*ibkr.MarketData, error)
}, db *store.DB) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			positions := bk.GetOpenPositions()
			if len(positions) == 0 {
				continue
			}

			prices := make(map[string]float64)
			for _, pos := range positions {
				md, err := client.ReqMarketData(ctx, pos.Instrument)
				if err != nil {
					slog.Warn("mark-to-market fetch failed", "symbol", pos.Instrument.Symbol, "error", err)
					continue
				}
				switch {
				case md.Last > 0:
					prices[pos.Instrument.Symbol] = md.Last
				case md.Bid > 0 && md.Ask > 0:
					prices[pos.Instrument.Symbol] = (md.Bid + md.Ask) / 2
				case md.Bid > 0:
					prices[pos.Instrument.Symbol] = md.Bid
				case md.Ask > 0:
					prices[pos.Instrument.Symbol] = md.Ask
				}
			}

			if len(prices) == 0 {
				continue
			}

			bk.Mark(prices)

			if db != nil {
				for _, pos := range bk.GetOpenPositions() {
					if err := db.UpsertPosition(ctx, pos); err != nil {
						slog.Warn("persist mark-to-market failed", "position_id", pos.ID, "error", err)
					}
				}
			}
		}
	}
}
