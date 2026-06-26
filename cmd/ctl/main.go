package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
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
	case "broker-orders":
		cmdBrokerOrders()
	case "broker-positions":
		cmdBrokerPositions()
	case "broker-probe-order":
		cmdBrokerProbeOrder()
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
	case "funnel":
		ctx, db := mustOpenDB()
		defer db.Close()
		cmdFunnel(ctx, db)
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
	fmt.Println("  broker-orders    Show open orders reported directly by IBKR/TWS")
	fmt.Println("  broker-positions Show positions reported directly by IBKR/TWS")
	fmt.Println("  broker-probe-order [SYMBOL] [--side buy|sell] [--qty N] [--type limit|market] [--limit P] [--keep-open]")
	fmt.Println("  positions     List all open positions from the database")
	fmt.Println("  theses        List recent theses with status")
	fmt.Println("  anti          Show anti-portfolio (rejected theses)")
	fmt.Println("  ab            Show A/B test comparison")
	fmt.Println("  events        Show recent event log entries")
	fmt.Println("  funnel [HRS]  Show scanner/rejection funnel for recent hours")
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

func diagnosticIBKRConfig() ibkr.Config {
	cfg := ibkr.DefaultConfig()
	if raw := strings.TrimSpace(os.Getenv("IBKR_DIAGNOSTIC_CLIENT_ID")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			cfg.ClientID = parsed
		}
	} else {
		cfg.ClientID += 50
	}
	if cfg.ClientIDTries < 10 {
		cfg.ClientIDTries = 10
	}
	return cfg
}

func cmdBrokerOrders() {
	allClients := true
	for _, arg := range os.Args[2:] {
		switch arg {
		case "--own-client":
			allClients = false
		case "--all-clients":
			allClients = true
		default:
			fmt.Fprintf(os.Stderr, "ctl broker-orders: unknown option %q\n", arg)
			fmt.Fprintf(os.Stderr, "usage: ctl broker-orders [--all-clients|--own-client]\n")
			os.Exit(1)
		}
	}

	cfg := diagnosticIBKRConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := ibkr.NewClient(cfg)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ctl broker-orders: connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	orders, err := client.OpenOrders(ctx, allClients)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctl broker-orders: %v\n", err)
		os.Exit(1)
	}
	if len(orders) == 0 {
		fmt.Println("No open broker orders.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !writeTablef(w, "ORDER\tPERM\tCLIENT\tSYMBOL\tSEC\tEXCHANGE\tCUR\tACTION\tTYPE\tQTY\tLMT\tAUX\tTIF\tOUT_RTH\tTX\tSTATUS\tFILLED\tREMAIN\tWHY_HELD\n") {
		return
	}
	for _, order := range orders {
		symbol := order.Symbol
		if order.LocalSymbol != "" && order.LocalSymbol != symbol {
			symbol = symbol + "(" + order.LocalSymbol + ")"
		}
		exchange := order.Exchange
		if order.PrimaryExchange != "" {
			exchange = exchange + "/" + order.PrimaryExchange
		}
		whyHeld := order.WhyHeld
		if whyHeld == "" {
			whyHeld = "-"
		}
		if !writeTablef(w, "%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%.4g\t%.4f\t%.4f\t%s\t%t\t%t\t%s\t%.4g\t%.4g\t%s\n",
			order.OrderID,
			order.PermID,
			order.ClientID,
			symbol,
			order.SecType,
			exchange,
			order.Currency,
			order.Action,
			order.OrderType,
			order.TotalQuantity,
			order.LmtPrice,
			order.AuxPrice,
			order.TIF,
			order.OutsideRTH,
			order.Transmit,
			order.Status,
			order.FilledQuantity,
			order.RemainingQuantity,
			whyHeld,
		) {
			return
		}
	}
	flushTable(w)
}

func cmdBrokerPositions() {
	cfg := diagnosticIBKRConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := ibkr.NewClient(cfg)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ctl broker-positions: connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	positions, err := client.GetPositions(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctl broker-positions: %v\n", err)
		os.Exit(1)
	}
	if len(positions) == 0 {
		fmt.Println("No broker positions.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !writeTablef(w, "CONID\tSYMBOL\tSEC\tEXCHANGE\tCUR\tQTY\tAVG_COST\tNOTIONAL\n") {
		return
	}
	for _, pos := range positions {
		if !writeTablef(w, "%d\t%s\t%s\t%s\t%s\t%.4g\t%.4f\t%.2f\n",
			pos.ConID,
			pos.Symbol,
			pos.SecType,
			pos.Exchange,
			pos.Currency,
			pos.Quantity,
			pos.AvgCost,
			pos.Quantity*pos.AvgCost,
		) {
			return
		}
	}
	flushTable(w)
}

func cmdBrokerProbeOrder() {
	symbol := "AAPL"
	side := model.Long
	qty := 1.0
	limit := 1.0
	orderType := model.OrderLimit
	keepOpen := false

	args := os.Args[2:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		symbol = strings.ToUpper(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--keep-open":
			keepOpen = true
		case "--market":
			orderType = model.OrderMarket
		case "--type":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "ctl broker-probe-order: --type requires limit or market")
				os.Exit(1)
			}
			switch strings.ToLower(strings.TrimSpace(args[i])) {
			case "limit", "lmt":
				orderType = model.OrderLimit
			case "market", "mkt":
				orderType = model.OrderMarket
			default:
				fmt.Fprintf(os.Stderr, "ctl broker-probe-order: unsupported type %q\n", args[i])
				os.Exit(1)
			}
		case "--side":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "ctl broker-probe-order: --side requires buy or sell")
				os.Exit(1)
			}
			switch strings.ToLower(strings.TrimSpace(args[i])) {
			case "buy", "long":
				side = model.Long
			case "sell", "short":
				side = model.Short
			default:
				fmt.Fprintf(os.Stderr, "ctl broker-probe-order: unsupported side %q\n", args[i])
				os.Exit(1)
			}
		case "--qty":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "ctl broker-probe-order: --qty requires a number")
				os.Exit(1)
			}
			parsed, err := strconv.ParseFloat(args[i], 64)
			if err != nil || parsed <= 0 {
				fmt.Fprintf(os.Stderr, "ctl broker-probe-order: invalid qty %q\n", args[i])
				os.Exit(1)
			}
			qty = parsed
		case "--limit":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "ctl broker-probe-order: --limit requires a number")
				os.Exit(1)
			}
			parsed, err := strconv.ParseFloat(args[i], 64)
			if err != nil || parsed <= 0 {
				fmt.Fprintf(os.Stderr, "ctl broker-probe-order: invalid limit %q\n", args[i])
				os.Exit(1)
			}
			limit = parsed
		default:
			fmt.Fprintf(os.Stderr, "ctl broker-probe-order: unknown option %q\n", args[i])
			fmt.Fprintf(os.Stderr, "usage: ctl broker-probe-order [SYMBOL] [--side buy|sell] [--qty N] [--type limit|market] [--limit P] [--keep-open]\n")
			os.Exit(1)
		}
	}
	if orderType == model.OrderMarket && keepOpen {
		fmt.Fprintln(os.Stderr, "ctl broker-probe-order: --keep-open is only valid for limit probes")
		os.Exit(1)
	}

	cfg := diagnosticIBKRConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client := ibkr.NewClient(cfg)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ctl broker-probe-order: connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	if !client.IsPaper() && !readProbeAllowLive() {
		fmt.Fprintln(os.Stderr, "ctl broker-probe-order: refusing to submit on a non-paper IBKR session; set IBKR_PROBE_ALLOW_LIVE=true to override")
		os.Exit(1)
	}
	if !client.IsPaper() && orderType == model.OrderMarket {
		fmt.Fprintln(os.Stderr, "ctl broker-probe-order: refusing live market probe even with override")
		os.Exit(1)
	}

	order := model.Order{
		ID:          "broker-probe-" + time.Now().UTC().Format("20060102T150405"),
		ThesisID:    "broker-probe",
		DeskID:      "ctl",
		Instrument:  model.Instrument{Symbol: symbol, SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:   side,
		Quantity:    qty,
		OrderType:   orderType,
		TimeInForce: "DAY",
	}
	if orderType == model.OrderLimit {
		order.LimitPrice = limit
	}

	fill, err := client.PlaceOrder(ctx, order)
	if err != nil {
		var pending *ibkr.PendingOrderError
		if !errors.As(err, &pending) {
			fmt.Fprintf(os.Stderr, "ctl broker-probe-order: submit failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Broker accepted probe order: broker_order_id=%d status=%s reason=%q\n", pending.OrderID, pending.Status, pending.Reason)
		if keepOpen {
			return
		}
		cancelCtx, cancelOrder := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelOrder()
		if err := client.CancelOrder(cancelCtx, pending.OrderID); err != nil {
			fmt.Fprintf(os.Stderr, "ctl broker-probe-order: cancel failed for broker_order_id=%d: %v\n", pending.OrderID, err)
			os.Exit(1)
		}
		fmt.Printf("Cancel requested for broker_order_id=%d\n", pending.OrderID)
		return
	}
	if fill == nil {
		fmt.Println("Probe completed without fill payload.")
		return
	}
	fmt.Printf("Probe filled: broker_order_id=%d symbol=%s qty=%.4g avg_price=%.4f\n", fill.IBKROrderID, fill.DisplaySymbol(), fill.Quantity, fill.AvgPrice)
}

func readProbeAllowLive() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("IBKR_PROBE_ALLOW_LIVE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
		nasdaq := marketdata.NewNasdaqProvider()
		if fallback, fallbackErr := marketdata.NewFMPProvider(""); fallbackErr == nil {
			provider := marketdata.NewFallbackProvider(marketdata.NewFallbackProvider(snapshotProvider, fallback), nasdaq)
			return "massive_free+polygon_agg_snapshots+fmp_fallback+nasdaq_quote", provider, nil
		}
		provider := marketdata.NewFallbackProvider(marketdata.NewSplitProvider(snapshotProvider, polygonProvider), nasdaq)
		return "massive_free+polygon_agg_snapshots+nasdaq_quote", provider, nil
	default:
		var provider marketdata.SnapshotProvider = polygonProvider
		providerName := "massive"
		if fallback, fallbackErr := marketdata.NewFMPProvider(""); fallbackErr == nil {
			provider = marketdata.NewFallbackProvider(provider, fallback)
			providerName = "massive+fmp_fallback"
		}
		provider = marketdata.NewFallbackProvider(provider, marketdata.NewNasdaqProvider())
		providerName += "+nasdaq_quote"
		return providerName, provider, nil
	}
}

func cmdPositions(ctx context.Context, db *store.DB) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, desk_id, instrument->>'symbol' AS symbol, direction, quantity,
		        entry_price, COALESCE(current_price, 0), COALESCE(unrealized_pnl, 0), status
		 FROM positions WHERE status = 'open' ORDER BY opened_at DESC LIMIT 50`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !writeTablef(w, "ID\tDESK\tSYMBOL\tDIR\tQTY\tENTRY\tCURRENT\tUPNL\tSTATUS\n") {
		return
	}
	for rows.Next() {
		var id, desk, symbol, dir, status string
		var qty, entry, current, upnl float64
		if err := rows.Scan(&id, &desk, &symbol, &dir, &qty, &entry, &current, &upnl, &status); err != nil {
			continue
		}
		if !writeTablef(w, "%s\t%s\t%s\t%s\t%.2f\t%.2f\t%.2f\t%.2f\t%s\n",
			id[:8], desk, symbol, dir, qty, entry, current, upnl, status) {
			return
		}
	}
	flushTable(w)
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
	if !writeTablef(w, "ID\tDESK\tSTRATEGY\tSYMBOL\tDIR\tCONV\tSTATUS\tPNL\n") {
		return
	}
	for rows.Next() {
		var id, desk, strategy, symbol, dir, status, pnl string
		var conviction float64
		if err := rows.Scan(&id, &desk, &strategy, &symbol, &dir, &conviction, &status, &pnl); err != nil {
			continue
		}
		if pnl == "" {
			pnl = "-"
		}
		if !writeTablef(w, "%s\t%s\t%s\t%s\t%s\t%.2f\t%s\t%s\n",
			id[:8], desk, strategy, symbol, dir, conviction, status, pnl) {
			return
		}
	}
	flushTable(w)
}

func cmdAntiPortfolio(ctx context.Context, db *store.DB) {
	rows, err := db.Pool.Query(ctx,
		`SELECT
		        COALESCE(thesis_id, thesis_snapshot->>'id', '') AS thesis_id,
		        desk_id,
		        rejection_reason,
		        COALESCE(metadata->>'stage', '') AS stage,
		        instrument->>'symbol' AS symbol,
		        direction,
		        conviction,
		        counterfactual_status,
		        would_have_pnl,
		        COALESCE(would_have_outcome, '')
		 FROM anti_portfolio ORDER BY created_at DESC LIMIT 30`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !writeTablef(w, "THESIS\tDESK\tSTAGE\tREASON\tSYMBOL\tDIR\tCONV\tCOUNTERFACTUAL\tWOULD_PNL\tOUTCOME\n") {
		return
	}
	for rows.Next() {
		var thesisID, desk, reason, stage, symbol, dir, counterfactual, outcome string
		var conviction, wouldPnl *float64
		if err := rows.Scan(&thesisID, &desk, &reason, &stage, &symbol, &dir, &conviction, &counterfactual, &wouldPnl, &outcome); err != nil {
			continue
		}
		if len(thesisID) > 8 {
			thesisID = thesisID[:8]
		}
		if thesisID == "" {
			thesisID = "-"
		}
		if stage == "" {
			stage = rejectionStageForReason(reason)
		}
		if outcome == "" {
			outcome = "-"
		}
		if !writeTablef(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			thesisID, desk, stage, reason, symbol, dir, formatOptionalFloat(conviction, 2), counterfactual, formatOptionalFloat(wouldPnl, 2), outcome) {
			return
		}
	}
	flushTable(w)
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

		if err := db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM theses WHERE desk_id LIKE $1`, prefix).Scan(&stats.Theses); err != nil {
			fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
			return
		}
		if err := db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM theses WHERE desk_id LIKE $1 AND status = 'active'`, prefix).Scan(&stats.Active); err != nil {
			fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
			return
		}
		if err := db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM theses WHERE desk_id LIKE $1 AND status = 'resolved'`, prefix).Scan(&stats.Resolved); err != nil {
			fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
			return
		}
		if err := db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM theses WHERE desk_id LIKE $1 AND status = 'resolved' AND (outcome->>'profitable')::boolean = true`, prefix).Scan(&stats.Profitable); err != nil {
			fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
			return
		}
		if err := db.Pool.QueryRow(ctx,
			`SELECT COALESCE(SUM((outcome->>'realized_pnl')::float), 0) FROM theses WHERE desk_id LIKE $1 AND status = 'resolved'`, prefix).Scan(&stats.TotalPnL); err != nil {
			fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
			return
		}

		out, _ := json.MarshalIndent(stats, "", "  ")
		fmt.Printf("Group %s:\n%s\n\n", g, string(out))
	}
}

func cmdEvents(ctx context.Context, db *store.DB) {
	rows, err := db.Pool.Query(ctx,
		`SELECT timestamp::text, event_type, COALESCE(desk_id, ''), severity, message
		 FROM event_log ORDER BY timestamp DESC LIMIT 30`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !writeTablef(w, "TIMESTAMP\tTYPE\tDESK\tSEVERITY\tMESSAGE\n") {
		return
	}
	for rows.Next() {
		var ts, eventType, desk, severity, msg string
		if err := rows.Scan(&ts, &eventType, &desk, &severity, &msg); err != nil {
			continue
		}
		if !writeTablef(w, "%s\t%s\t%s\t%s\t%s\n", ts[:19], eventType, desk, severity, msg) {
			return
		}
	}
	flushTable(w)
}

func rejectionStageForReason(reason string) string {
	switch reason {
	case "conviction_below_threshold":
		return "research"
	case "killed_by_prosecutor", "prosecutor_weakened_below_threshold":
		return "prosecutor"
	case "council_rejected":
		return "council"
	case "blocked_by_runtime_health":
		return "runtime_health"
	case "blocked_by_risk_gate":
		return "risk"
	case "kalshi_executor_unavailable", "kalshi_execution_rejected", "kalshi_order_not_filled":
		return "execution"
	default:
		return "unknown"
	}
}

func cmdFunnel(ctx context.Context, db *store.DB) {
	hours := 24
	if len(os.Args) >= 3 {
		if parsed, err := strconv.Atoi(os.Args[2]); err == nil && parsed > 0 && parsed <= 720 {
			hours = parsed
		}
	}

	fmt.Printf("Decision funnel (last %dh)\n\n", hours)
	printFunnelEvents(ctx, db, hours)
	fmt.Println()
	printThesisStatusCounts(ctx, db, hours)
	fmt.Println()
	printWorkingOrderCounts(ctx, db)
}

func printFunnelEvents(ctx context.Context, db *store.DB, hours int) {
	rows, err := db.Pool.Query(ctx, `
		SELECT
			COALESCE(metadata->>'stage', CASE WHEN event_type = 'scanner_rejected' THEN 'scanner' ELSE 'unknown' END) AS stage,
			event_type,
			COALESCE(metadata->>'scanner_reason', metadata->>'rejection_reason', '') AS reason,
			COUNT(*) AS count,
			AVG(NULLIF(metadata->>'scanner_score', '')::float8) AS avg_score,
			AVG(NULLIF(metadata->>'conviction', '')::float8) AS avg_conviction
		FROM event_log
		WHERE timestamp >= NOW() - ($1::int * INTERVAL '1 hour')
		  AND event_type IN ('scanner_rejected', 'thesis_rejected')
		GROUP BY stage, event_type, reason
		ORDER BY count DESC, stage, reason
	`, hours)
	if err != nil {
		fmt.Fprintf(os.Stderr, "funnel events query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !writeTablef(w, "STAGE\tEVENT\tREASON\tCOUNT\tAVG_SCORE\tAVG_CONV\n") {
		return
	}
	for rows.Next() {
		var stage, eventType, reason string
		var count int
		var avgScore, avgConviction *float64
		if err := rows.Scan(&stage, &eventType, &reason, &count, &avgScore, &avgConviction); err != nil {
			continue
		}
		if reason == "" {
			reason = "-"
		}
		if !writeTablef(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
			stage, eventType, reason, count, formatOptionalFloat(avgScore, 1), formatOptionalFloat(avgConviction, 2)) {
			return
		}
	}
	flushTable(w)
}

func printThesisStatusCounts(ctx context.Context, db *store.DB, hours int) {
	rows, err := db.Pool.Query(ctx, `
		SELECT status, COUNT(*), AVG(conviction)
		FROM theses
		WHERE created_at >= NOW() - ($1::int * INTERVAL '1 hour')
		GROUP BY status
		ORDER BY COUNT(*) DESC
	`, hours)
	if err != nil {
		fmt.Fprintf(os.Stderr, "thesis status query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !writeTablef(w, "THESIS_STATUS\tCOUNT\tAVG_CONV\n") {
		return
	}
	for rows.Next() {
		var status string
		var count int
		var avgConviction *float64
		if err := rows.Scan(&status, &count, &avgConviction); err != nil {
			continue
		}
		if !writeTablef(w, "%s\t%d\t%s\n", status, count, formatOptionalFloat(avgConviction, 2)) {
			return
		}
	}
	flushTable(w)
}

func printWorkingOrderCounts(ctx context.Context, db *store.DB) {
	rows, err := db.Pool.Query(ctx, `
		SELECT state, COUNT(*)
		FROM working_orders
		GROUP BY state
		ORDER BY COUNT(*) DESC
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "working orders query failed: %v\n", err)
		return
	}
	defer rows.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if !writeTablef(w, "ORDER_STATE\tCOUNT\n") {
		return
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			continue
		}
		if !writeTablef(w, "%s\t%d\n", state, count) {
			return
		}
	}
	flushTable(w)
}

func formatOptionalFloat(value *float64, digits int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.*f", digits, *value)
}

func writeTablef(w *tabwriter.Writer, format string, args ...any) bool {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "write failed: %v\n", err)
		return false
	}
	return true
}

func flushTable(w *tabwriter.Writer) {
	if err := w.Flush(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "flush failed: %v\n", err)
	}
}
