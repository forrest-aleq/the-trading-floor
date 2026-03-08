package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/internal/wire/feeds"
)

func main() {
	// Structured logging
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("=== THE TRADING FLOOR ===")
	slog.Info("initializing autonomous trading system")

	// ── LLM Router ──────────────────────────────────────────────
	llmRouter := llm.DefaultRouter()
	slog.Info("LLM router initialized",
		"speed_model", os.Getenv("LLM_MODEL_SPEED"),
		"analysis_model", os.Getenv("LLM_MODEL_ANALYSIS"),
		"critical_model", os.Getenv("LLM_MODEL_CRITICAL"),
	)

	// ── IBKR Connection ─────────────────────────────────────────
	ibkrCfg := ibkr.DefaultConfig()
	ibkrClient := ibkr.NewClient(ibkrCfg)
	if err := ibkrClient.Connect(ctx); err != nil {
		slog.Error("IBKR connection failed", "error", err)
		os.Exit(1)
	}
	defer ibkrClient.Close()
	slog.Info("IBKR connected", "config", ibkrClient.IsPaper())

	// ── Pillars ─────────────────────────────────────────────────

	// Execution
	execMgr := execution.NewManager(ibkrClient)

	// Book ($1M paper capital)
	bk := book.NewBook(ibkrClient, 1_000_000)
	go bk.StartReconcile(ctx)

	// Risk Gate
	riskGate := risk.NewGate(risk.DefaultLimits())

	// Belief Graph
	beliefGraph := belief.NewGraph()

	// Learn Worker
	learnWorker := memory.NewLearnWorker(beliefGraph)
	_ = learnWorker // Used in desk process loop

	// Scanner
	scan := scanner.NewEngine(llmRouter, 40)

	// Research Desk
	researchDesk := research.NewDesk(llmRouter, 0.65)

	// Prosecutor
	prosecutor := research.NewProsecutor(llmRouter)

	// Audit Log
	audit, err := observe.NewAuditLog("audit.jsonl")
	if err != nil {
		slog.Error("audit log init failed", "error", err)
		os.Exit(1)
	}
	defer audit.Close()
	audit.Record("system_start", "", "", map[string]interface{}{
		"paper": ibkrClient.IsPaper(),
		"capital": 1_000_000,
	})

	// ── Wire ────────────────────────────────────────────────────
	wireMgr := wire.NewManager()
	wireMgr.RegisterFeed(feeds.NewNewsFeed(nil))
	// TODO: Add more feeds: IBKR market data, SEC EDGAR, social, Telegram

	// ── Firm: Desks ─────────────────────────────────────────────
	floor := firm.NewFloor(wireMgr)

	// Create initial desks — start with 5 desks across key domains
	domains := []struct {
		id      string
		domain  string
		group   string
	}{
		{"geo-a1", "geopolitical", "A"},
		{"macro-a1", "macro", "A"},
		{"corp-a1", "corporate", "A"},
		{"vol-a1", "volatility", "A"},
		{"sector-a1", "sector", "A"},
		// Group B controls (same domains, no beliefs)
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
		})
		floor.AddDesk(desk)
	}

	slog.Info("firm initialized",
		"desks", len(domains),
		"group_a", 5,
		"group_b", 5,
	)

	// ── Run ─────────────────────────────────────────────────────
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
	)

	fmt.Println("trading-floor: shutdown complete")
}
