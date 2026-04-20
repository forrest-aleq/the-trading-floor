package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/marketdata"
	"github.com/hnic/trading-floor/internal/store"
	"github.com/hnic/trading-floor/pkg/model"
)

func main() {
	_ = godotenv.Load()

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "market":
		cmdMarket()
	case "positions":
		ctx, db := mustOpenDB()
		defer db.Close()
		cmdPositions(ctx, db)
	case "theses":
		ctx, db := mustOpenDB()
		defer db.Close()
		cmdTheses(ctx, db)
	case "anti":
		ctx, db := mustOpenDB()
		defer db.Close()
		cmdAntiPortfolio(ctx, db)
	case "ab":
		ctx, db := mustOpenDB()
		defer db.Close()
		cmdABTest(ctx, db)
	case "events":
		ctx, db := mustOpenDB()
		defer db.Close()
		cmdEvents(ctx, db)
	default:
		fmt.Fprintf(os.Stderr, "ctl: unknown command '%s'\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("usage: ctl <command>")
	fmt.Println()
	fmt.Println("commands:")
	fmt.Println("  market [SYMBOL]  Show live market data from the configured market data provider")
	fmt.Println("  positions     List all open positions from the database")
	fmt.Println("  theses        List recent theses with status")
	fmt.Println("  anti          Show anti-portfolio (rejected theses)")
	fmt.Println("  ab            Show A/B test comparison")
	fmt.Println("  events        Show recent event log entries")
}

func mustOpenDB() (context.Context, *store.DB) {
	ctx := context.Background()
	db, err := store.NewDB(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctl: cannot connect to database: %v\n", err)
		fmt.Fprintf(os.Stderr, "set DATABASE_URL or create a .env file\n")
		os.Exit(1)
	}
	return ctx, db
}

func cmdMarket() {
	symbol := "SPY"
	if len(os.Args) >= 3 && os.Args[2] != "" {
		symbol = os.Args[2]
	}

	providerName, provider, err := loadCTLMarketProvider()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctl market: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	inst := model.Instrument{
		Symbol:   symbol,
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
	}
	if symbol == "VIX" {
		inst.SecType = "IND"
		inst.Exchange = "CBOE"
	}

	snapshot, err := provider.Snapshot(ctx, inst)
	if err != nil {
		historyProvider, ok := provider.(marketdata.HistoricalProvider)
		if !ok {
			fmt.Fprintf(os.Stderr, "ctl market: fetch %s failed: %v\n", symbol, err)
			os.Exit(1)
		}
		bars, historyErr := historyProvider.HistoricalBars(ctx, inst, time.Now().UTC(), "5 D", "1 day", "", true)
		if historyErr != nil || len(bars) == 0 {
			fmt.Fprintf(os.Stderr, "ctl market: fetch %s failed: %v\n", symbol, err)
			if historyErr != nil {
				fmt.Fprintf(os.Stderr, "ctl market: historical fallback failed: %v\n", historyErr)
			}
			os.Exit(1)
		}
		lastBar := bars[len(bars)-1]
		snapshot = &marketdata.Snapshot{
			Symbol:     symbol,
			Last:       lastBar.Close,
			ObservedAt: lastBar.Time,
		}
		providerName += "+historical_fallback"
	}

	out, _ := json.MarshalIndent(map[string]any{
		"provider":    providerName,
		"symbol":      symbol,
		"last":        snapshot.Last,
		"bid":         snapshot.Bid,
		"ask":         snapshot.Ask,
		"volume":      snapshot.Volume,
		"observed_at": snapshot.ObservedAt,
	}, "", "  ")
	fmt.Println(string(out))
}

func loadCTLMarketProvider() (string, marketdata.SnapshotProvider, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("MARKET_DATA_PROVIDER")))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(os.Getenv("MARKET_STATE_PROVIDER")))
	}
	if raw == "" {
		raw = marketdata.ResolveDefaultMarketDataProvider()
	}
	if raw == "" || raw == "none" {
		return "", nil, fmt.Errorf("no market data provider configured; set MARKET_DATA_PROVIDER=fmp|polygon or provide API credentials for auto-detection")
	}

	switch raw {
	case "massive":
		raw = "polygon"
	}

	switch raw {
	case "fmp":
		provider, err := marketdata.NewFMPProvider("")
		return raw, provider, err
	case "polygon":
		return loadCTLMassiveBackedProvider()
	default:
		return "", nil, fmt.Errorf("unsupported MARKET_DATA_PROVIDER %q", raw)
	}
}

func loadCTLMassiveBackedProvider() (string, marketdata.SnapshotProvider, error) {
	polygonProvider, err := marketdata.NewPolygonProvider("")
	if err != nil {
		return "", nil, err
	}

	switch marketdata.ResolveMassivePlan() {
	case marketdata.MassivePlanBasicFree:
		snapshotProvider := marketdata.NewHistoricalSnapshotProvider(polygonProvider)
		return "massive_free+polygon_agg_snapshots", marketdata.NewSplitProvider(snapshotProvider, polygonProvider), nil
	default:
		var provider marketdata.SnapshotProvider = polygonProvider
		providerName := "massive"
		if fallback, fallbackErr := marketdata.NewFMPProvider(""); fallbackErr == nil {
			provider = marketdata.NewFallbackProvider(provider, fallback)
			providerName = "massive+fmp_fallback"
		}
		return providerName, provider, nil
	}
}

func cmdPositions(ctx context.Context, db *store.DB) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, desk_id, instrument->>'symbol' AS symbol, direction, quantity,
		        entry_price, COALESCE(current_price, 0), COALESCE(unrealized_pnl, 0), status
		 FROM positions ORDER BY opened_at DESC LIMIT 50`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDESK\tSYMBOL\tDIR\tQTY\tENTRY\tCURRENT\tUPNL\tSTATUS")
	for rows.Next() {
		var id, desk, symbol, dir, status string
		var qty, entry, current, upnl float64
		if err := rows.Scan(&id, &desk, &symbol, &dir, &qty, &entry, &current, &upnl, &status); err != nil {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.2f\t%.2f\t%.2f\t%.2f\t%s\n",
			id[:8], desk, symbol, dir, qty, entry, current, upnl, status)
	}
	w.Flush()
}

func cmdTheses(ctx context.Context, db *store.DB) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, desk_id, strategy, instrument->>'symbol' AS symbol, direction,
		        conviction, status, COALESCE(outcome->>'realized_pnl', '') AS pnl
		 FROM theses ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDESK\tSTRATEGY\tSYMBOL\tDIR\tCONV\tSTATUS\tPNL")
	for rows.Next() {
		var id, desk, strategy, symbol, dir, status, pnl string
		var conviction float64
		if err := rows.Scan(&id, &desk, &strategy, &symbol, &dir, &conviction, &status, &pnl); err != nil {
			continue
		}
		if pnl == "" {
			pnl = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%.2f\t%s\t%s\n",
			id[:8], desk, strategy, symbol, dir, conviction, status, pnl)
	}
	w.Flush()
}

func cmdAntiPortfolio(ctx context.Context, db *store.DB) {
	rows, err := db.Pool.Query(ctx,
		`SELECT desk_id, rejection_reason, strategy, instrument->>'symbol' AS symbol, direction,
		        COALESCE(would_have_pnl, 0), COALESCE(would_have_outcome, '')
		 FROM anti_portfolio ORDER BY created_at DESC LIMIT 30`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DESK\tREASON\tSTRATEGY\tSYMBOL\tDIR\tWOULD_PNL\tOUTCOME")
	for rows.Next() {
		var desk, reason, strategy, symbol, dir, outcome string
		var wouldPnl float64
		if err := rows.Scan(&desk, &reason, &strategy, &symbol, &dir, &wouldPnl, &outcome); err != nil {
			continue
		}
		if outcome == "" {
			outcome = "pending"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%.2f\t%s\n",
			desk, reason, strategy, symbol, dir, wouldPnl, outcome)
	}
	w.Flush()
}

func cmdABTest(ctx context.Context, db *store.DB) {
	type groupStats struct {
		Group      string  `json:"group"`
		Theses     int     `json:"theses"`
		Active     int     `json:"active"`
		Resolved   int     `json:"resolved"`
		Profitable int     `json:"profitable"`
		TotalPnL   float64 `json:"total_pnl"`
	}

	groups := []string{"A", "B"}
	for _, g := range groups {
		var stats groupStats
		stats.Group = g

		prefix := "%-a"
		if g == "B" {
			prefix = "%-b"
		}

		db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM theses WHERE desk_id LIKE $1`, prefix).Scan(&stats.Theses)
		db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM theses WHERE desk_id LIKE $1 AND status = 'active'`, prefix).Scan(&stats.Active)
		db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM theses WHERE desk_id LIKE $1 AND status = 'resolved'`, prefix).Scan(&stats.Resolved)
		db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM theses WHERE desk_id LIKE $1 AND status = 'resolved' AND (outcome->>'profitable')::boolean = true`, prefix).Scan(&stats.Profitable)
		db.Pool.QueryRow(ctx,
			`SELECT COALESCE(SUM((outcome->>'realized_pnl')::float), 0) FROM theses WHERE desk_id LIKE $1 AND status = 'resolved'`, prefix).Scan(&stats.TotalPnL)

		out, _ := json.MarshalIndent(stats, "", "  ")
		fmt.Printf("Group %s:\n%s\n\n", g, string(out))
	}
}

func cmdEvents(ctx context.Context, db *store.DB) {
	rows, err := db.Pool.Query(ctx,
		`SELECT timestamp, event_type, COALESCE(desk_id, ''), severity, message
		 FROM event_log ORDER BY timestamp DESC LIMIT 30`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIMESTAMP\tTYPE\tDESK\tSEVERITY\tMESSAGE")
	for rows.Next() {
		var ts, eventType, desk, severity, msg string
		if err := rows.Scan(&ts, &eventType, &desk, &severity, &msg); err != nil {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", ts[:19], eventType, desk, severity, msg)
	}
	w.Flush()
}
