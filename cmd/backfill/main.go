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

	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/store"
)

type options struct {
	limit         int
	since         time.Duration
	mode          string
	domains       map[string]struct{}
	signalTimeout time.Duration
}

type replaySummary struct {
	Mode              string                 `json:"mode"`
	SignalsLoaded     int                    `json:"signals_loaded"`
	SignalsReplayed   int                    `json:"signals_replayed"`
	DomainEvaluations int                    `json:"domain_evaluations"`
	Opportunities     int                    `json:"opportunities"`
	Theses            int                    `json:"theses"`
	ResearchErrors    map[string]int         `json:"research_errors,omitempty"`
	Domains           map[string]domainStats `json:"domains"`
	Since             string                 `json:"since"`
	GeneratedAt       time.Time              `json:"generated_at"`
}

type domainStats struct {
	Signals       int `json:"signals"`
	Opportunities int `json:"opportunities"`
	Theses        int `json:"theses"`
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
	scan := scanner.NewEngine(router, 70)
	researchDesk := research.NewDesk(router, 0.65)

	summary := replaySummary{
		Mode:           opts.mode,
		SignalsLoaded:  len(signals),
		ResearchErrors: map[string]int{},
		Domains:        map[string]domainStats{},
		Since:          opts.since.String(),
		GeneratedAt:    time.Now().UTC(),
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
			opp, ok := scan.Evaluate(signalCtx, sig, domain)
			cancel()
			if !ok || opp == nil {
				summary.Domains[domain] = stats
				continue
			}

			stats.Opportunities++
			summary.Opportunities++

			if opts.mode == "research" {
				researchCtx, cancel := context.WithTimeout(ctx, opts.signalTimeout)
				thesis, err := researchDesk.Investigate(researchCtx, opp, sig, backfillDeskID(domain))
				cancel()
				if err != nil {
					summary.ResearchErrors[classifyReplayError(err)]++
					summary.Domains[domain] = stats
					continue
				}
				if thesis != nil {
					stats.Theses++
					summary.Theses++
				}
			}

			summary.Domains[domain] = stats
		}
	}

	if len(summary.ResearchErrors) == 0 {
		summary.ResearchErrors = nil
	}

	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "backfill: encode summary: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}

func parseOptions() options {
	var (
		limit         = flag.Int("limit", 200, "maximum signals to replay from persistence")
		since         = flag.Duration("since", 7*24*time.Hour, "time window to replay")
		mode          = flag.String("mode", "research", "replay mode: scan or research")
		domains       = flag.String("domains", "", "comma-separated domains to replay")
		signalTimeout = flag.Duration("signal-timeout", 45*time.Second, "per-signal evaluation timeout")
	)
	flag.Parse()

	return options{
		limit:         *limit,
		since:         *since,
		mode:          normalizeReplayMode(*mode),
		domains:       parseDomainFilter(*domains),
		signalTimeout: *signalTimeout,
	}
}

func normalizeReplayMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "scan":
		return "scan"
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
		return domains
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
