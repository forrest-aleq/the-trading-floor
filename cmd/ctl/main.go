package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/store"
)

func main() {
	_ = godotenv.Load()

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := store.NewDB(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctl: cannot connect to database: %v\n", err)
		fmt.Fprintf(os.Stderr, "set DATABASE_URL or create a .env file\n")
		os.Exit(1)
	}
	defer db.Close()

	switch os.Args[1] {
	case "positions":
		cmdPositions(ctx, db)
	case "theses":
		cmdTheses(ctx, db)
	case "anti":
		cmdAntiPortfolio(ctx, db)
	case "ab":
		cmdABTest(ctx, db)
	case "events":
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
	fmt.Println("  positions     List all open positions from the database")
	fmt.Println("  theses        List recent theses with status")
	fmt.Println("  anti          Show anti-portfolio (rejected theses)")
	fmt.Println("  ab            Show A/B test comparison")
	fmt.Println("  events        Show recent event log entries")
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
