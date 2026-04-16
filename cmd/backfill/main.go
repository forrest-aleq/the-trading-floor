package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/backtest"
	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/store"
	"github.com/hnic/trading-floor/pkg/model"
)

type options struct {
	limit         int
	since         time.Duration
	mode          string
	domains       map[string]struct{}
	signalTimeout time.Duration
	capital       float64
	applyBeliefs  bool
	minScore      float64
	minConviction float64
}

type replaySummary struct {
	Mode                string                 `json:"mode"`
	SignalsLoaded       int                    `json:"signals_loaded"`
	SignalsReplayed     int                    `json:"signals_replayed"`
	DomainEvaluations   int                    `json:"domain_evaluations"`
	Opportunities       int                    `json:"opportunities"`
	Theses              int                    `json:"theses"`
	Backtested          int                    `json:"backtested"`
	ProfitableOutcomes  int                    `json:"profitable_outcomes"`
	TotalPnL            float64                `json:"total_pnl"`
	HistoricalAvailable bool                   `json:"historical_available"`
	BeliefsGenerated    int                    `json:"beliefs_generated,omitempty"`
	EngramsGenerated    int                    `json:"engrams_generated,omitempty"`
	MinScore            float64                `json:"min_score"`
	MinConviction       float64                `json:"min_conviction"`
	ScanRejects         map[string]int         `json:"scan_rejects,omitempty"`
	ResearchRejects     map[string]int         `json:"research_rejects,omitempty"`
	ResearchErrors      map[string]int         `json:"research_errors,omitempty"`
	BacktestErrors      map[string]int         `json:"backtest_errors,omitempty"`
	Warnings            []string               `json:"warnings,omitempty"`
	Domains             map[string]domainStats `json:"domains"`
	Since               string                 `json:"since"`
	GeneratedAt         time.Time              `json:"generated_at"`
}

type domainStats struct {
	Signals            int            `json:"signals"`
	Opportunities      int            `json:"opportunities"`
	Theses             int            `json:"theses"`
	Backtested         int            `json:"backtested"`
	ProfitableOutcomes int            `json:"profitable_outcomes"`
	TotalPnL           float64        `json:"total_pnl"`
	ScanRejects        map[string]int `json:"scan_rejects,omitempty"`
	ResearchRejects    map[string]int `json:"research_rejects,omitempty"`
}

func main() {
	_ = godotenv.Load()

	opts := parseOptions()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(os.Getenv("LOG_LEVEL")),
	})))

	ctx := context.Background()
	db, err := store.NewDB(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backfill: connect postgres: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	signals, err := db.ListSignals(ctx, store.SignalQuery{
		Limit: opts.limit,
		Since: time.Now().Add(-opts.since),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "backfill: load signals: %v\n", err)
		os.Exit(1)
	}

	router := llm.DefaultRouter()
	scan := scanner.NewEngine(router, opts.minScore)
	researchDesk := research.NewDesk(router, opts.minConviction)
	historical := maybeConnectHistoricalBroker(ctx)
	if historical != nil {
		defer historical.Close()
	}
	var (
		beliefGraph *belief.Graph
		engramStore *memory.EngramStore
		learnWorker *memory.LearnWorker
	)
	if opts.applyBeliefs {
		beliefGraph = belief.NewGraph()
		engramStore = memory.NewEngramStore()
		learnWorker = memory.NewLearnWorker(beliefGraph, engramStore)
	}

	summary := replaySummary{
		Mode:                opts.mode,
		SignalsLoaded:       len(signals),
		HistoricalAvailable: historical != nil,
		MinScore:            opts.minScore,
		MinConviction:       opts.minConviction,
		ScanRejects:         map[string]int{},
		ResearchRejects:     map[string]int{},
		ResearchErrors:      map[string]int{},
		BacktestErrors:      map[string]int{},
		Domains:             map[string]domainStats{},
		Since:               opts.since.String(),
		GeneratedAt:         time.Now().UTC(),
	}
	if historical == nil && opts.mode == "backtest" {
		summary.Warnings = append(summary.Warnings, "historical data unavailable; falling back to research-only replay")
	}

	for _, sig := range signals {
		domains := firm.RelevantDomainsForSignal(sig)
		if len(domains) == 0 {
			continue
		}
		domains = filterDomains(domains, opts.domains)
		if len(domains) == 0 {
			continue
		}

		summary.SignalsReplayed++
		for _, domain := range domains {
			stats := summary.Domains[domain]
			stats.Signals++
			summary.DomainEvaluations++

			signalCtx, cancel := context.WithTimeout(ctx, opts.signalTimeout)
			scanCtx := scanner.WithEvaluationTime(signalCtx, sig.Timestamp)
			scanResult := scan.EvaluateDetailed(scanCtx, sig, domain)
			cancel()
			if !scanResult.Accepted || scanResult.Opportunity == nil {
				recordScanReject(&summary, &stats, scanResult.Reason)
				summary.Domains[domain] = stats
				continue
			}
			opp := scanResult.Opportunity

			stats.Opportunities++
			summary.Opportunities++

			if opts.mode == "research" || opts.mode == "backtest" {
				researchCtx, cancel := context.WithTimeout(ctx, opts.signalTimeout)
				investigation, err := researchDesk.InvestigateDetailed(researchCtx, opp, sig, backfillDeskID(domain))
				cancel()
				if err != nil {
					recordResearchReject(&summary, &stats, investigation.Reason)
					summary.ResearchErrors[classifyReplayError(err)]++
					summary.Domains[domain] = stats
					continue
				}
				if !investigation.Accepted || investigation.Thesis == nil {
					recordResearchReject(&summary, &stats, investigation.Reason)
					summary.Domains[domain] = stats
					continue
				}
				thesis := investigation.Thesis
				if thesis != nil {
					stats.Theses++
					summary.Theses++
					if opts.mode == "backtest" && historical != nil {
						evaluated := cloneThesis(thesis)
						backtest.NormalizePositionSize(evaluated, opts.capital)
						plan := backtest.BuildHistoricalPlan(sig.Timestamp, evaluated)

						historyCtx, cancel := context.WithTimeout(ctx, opts.signalTimeout)
						bars, err := historical.HistoricalBars(historyCtx, evaluated.PrimaryInstrument(), plan.EndTime, plan.Duration, plan.BarSize, plan.WhatToShow, plan.UseRTH)
						cancel()
						if err != nil {
							summary.BacktestErrors[classifyReplayError(err)]++
							summary.Domains[domain] = stats
							continue
						}

						outcome, err := backtest.EvaluateHistoricalOutcome(evaluated, plan.EntryTime, bars)
						if err != nil {
							summary.BacktestErrors[classifyReplayError(err)]++
							summary.Domains[domain] = stats
							continue
						}
						stats.Backtested++
						summary.Backtested++
						stats.TotalPnL += outcome.RealizedPnL
						summary.TotalPnL += outcome.RealizedPnL
						if outcome.Profitable {
							stats.ProfitableOutcomes++
							summary.ProfitableOutcomes++
						}
						if learnWorker != nil {
							evaluated.Outcome = outcome
							learnWorker.ProcessOutcome(evaluated, outcome, defaultReplayRegime())
						}
					}
				}
			}

			summary.Domains[domain] = stats
		}
	}

	if len(summary.ResearchErrors) == 0 {
		summary.ResearchErrors = nil
	}
	if len(summary.ResearchRejects) == 0 {
		summary.ResearchRejects = nil
	}
	if len(summary.ScanRejects) == 0 {
		summary.ScanRejects = nil
	}
	if len(summary.BacktestErrors) == 0 {
		summary.BacktestErrors = nil
	}
	if len(summary.Warnings) == 0 {
		summary.Warnings = nil
	}
	if beliefGraph != nil {
		summary.BeliefsGenerated = len(beliefGraph.All())
	}
	if engramStore != nil {
		summary.EngramsGenerated = len(engramStore.All())
	}

	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "backfill: encode summary: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}

func recordScanReject(summary *replaySummary, stats *domainStats, reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	summary.ScanRejects[reason]++
	if stats.ScanRejects == nil {
		stats.ScanRejects = map[string]int{}
	}
	stats.ScanRejects[reason]++
}

func recordResearchReject(summary *replaySummary, stats *domainStats, reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}
	summary.ResearchRejects[reason]++
	if stats.ResearchRejects == nil {
		stats.ResearchRejects = map[string]int{}
	}
	stats.ResearchRejects[reason]++
}

func parseOptions() options {
	var (
		limit         = flag.Int("limit", 200, "maximum signals to replay from persistence")
		since         = flag.Duration("since", 7*24*time.Hour, "time window to replay")
		mode          = flag.String("mode", "research", "replay mode: scan or research")
		domains       = flag.String("domains", "", "comma-separated domains to replay")
		signalTimeout = flag.Duration("signal-timeout", 45*time.Second, "per-signal evaluation timeout")
		capital       = flag.Float64("capital", 100000, "desk capital assumption used to normalize thesis position sizing for replay/backtest")
		applyBeliefs  = flag.Bool("apply-beliefs", false, "apply backtested outcomes to a fresh in-memory belief graph and engram store")
		minScore      = flag.Float64("min-score", 70, "scanner minimum score threshold for replay")
		minConviction = flag.Float64("min-conviction", 0.65, "research minimum conviction threshold for replay")
	)
	flag.Parse()

	return options{
		limit:         *limit,
		since:         *since,
		mode:          normalizeReplayMode(*mode),
		domains:       parseDomainFilter(*domains),
		signalTimeout: *signalTimeout,
		capital:       *capital,
		applyBeliefs:  *applyBeliefs,
		minScore:      *minScore,
		minConviction: *minConviction,
	}
}

func normalizeReplayMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "scan":
		return "scan"
	case "backtest":
		return "backtest"
	default:
		return "research"
	}
}

func parseDomainFilter(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	values := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		values[part] = struct{}{}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func filterDomains(domains []string, allow map[string]struct{}) []string {
	if len(allow) == 0 {
		sorted := append([]string(nil), domains...)
		sort.Strings(sorted)
		return sorted
	}
	filtered := make([]string, 0, len(domains))
	for _, domain := range domains {
		if _, ok := allow[domain]; ok {
			filtered = append(filtered, domain)
		}
	}
	sort.Strings(filtered)
	return filtered
}

func backfillDeskID(domain string) string {
	return fmt.Sprintf("backfill-%s", strings.TrimSpace(strings.ToLower(domain)))
}

func classifyReplayError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "json extraction"):
		return "json_extraction"
	case strings.Contains(msg, "validation"):
		return "validation"
	case strings.Contains(msg, "parse error"):
		return "parse_error"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"):
		return "timeout"
	default:
		return "other"
	}
}

func maybeConnectHistoricalBroker(ctx context.Context) *ibkr.Client {
	client := ibkr.NewClient(ibkr.DefaultConfig())
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return nil
	}
	return client
}

func cloneThesis(thesis *model.Thesis) *model.Thesis {
	if thesis == nil {
		return nil
	}
	cloned := *thesis
	cloned.Evidence = append([]model.Evidence(nil), thesis.Evidence...)
	cloned.CounterArgs = append([]string(nil), thesis.CounterArgs...)
	cloned.Legs = append([]model.TradeLeg(nil), thesis.Legs...)
	return &cloned
}

func defaultReplayRegime() model.Regime {
	return model.Regime{
		Volatility: "medium",
		Trend:      "neutral",
		Risk:       "neutral",
		Liquidity:  "normal",
	}
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
