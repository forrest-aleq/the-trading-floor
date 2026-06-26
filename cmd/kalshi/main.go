package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/execution/kalshi"
)

const liveConfirmation = kalshi.LiveConfirmation

func main() {
	_ = godotenv.Load()

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := kalshi.NewClient(kalshi.DefaultConfig())
	if err != nil {
		exitf("kalshi config: %v", err)
	}

	switch os.Args[1] {
	case "balance":
		cmdBalance(ctx, client, os.Args[2:])
	case "status":
		cmdStatus(ctx, client, os.Args[2:])
	case "markets":
		cmdMarkets(ctx, client, os.Args[2:])
	case "orderbook":
		cmdOrderbook(ctx, client, os.Args[2:])
	case "orders":
		cmdOrders(ctx, client, os.Args[2:])
	case "cancel-order":
		cmdCancelOrder(ctx, client, os.Args[2:])
	case "positions":
		cmdPositions(ctx, client, os.Args[2:])
	case "resting-value":
		cmdRestingValue(ctx, client, os.Args[2:])
	case "validate-order":
		cmdOrder(ctx, client, os.Args[2:], false)
	case "order":
		cmdOrder(ctx, client, os.Args[2:], true)
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("usage: kalshi <command>")
	fmt.Println()
	fmt.Println("commands:")
	fmt.Println("  status                    Show Kalshi exchange status")
	fmt.Println("  balance                   Show authenticated account balance")
	fmt.Println("  markets [--status open]   List markets")
	fmt.Println("  orderbook --ticker T      Show market orderbook")
	fmt.Println("  orders [--status resting] Show authenticated account orders")
	fmt.Println("  cancel-order --order-id O Cancel one authenticated account order")
	fmt.Println("  positions                 Show authenticated account positions")
	fmt.Println("  resting-value             Show total live dollars reserved by resting orders")
	fmt.Println("  validate-order ...        Validate risk and payload only")
	fmt.Println("  order ...                 Place a real order with explicit confirmation")
	fmt.Println()
	fmt.Println("order flags:")
	fmt.Printf("  --ticker T --side yes|no --action buy|sell --count 1 --price 0.1200 --live-confirm %s\n", liveConfirmation)
	fmt.Println("  side=yes/action=buy buys YES; side=no/action=buy buys NO")
}

func cmdBalance(ctx context.Context, client *kalshi.Client, args []string) {
	fs := flag.NewFlagSet("balance", flag.ExitOnError)
	_ = fs.Parse(args)

	balance, err := client.GetBalance(ctx)
	if err != nil {
		exitf("balance failed: %v", err)
	}
	fmt.Printf("available_balance=%s portfolio_value=%s updated_ts=%d\n",
		kalshi.FormatCents(balance.Balance),
		kalshi.FormatCents(balance.PortfolioValue),
		balance.UpdatedTS,
	)
}

func cmdStatus(ctx context.Context, client *kalshi.Client, args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	_ = fs.Parse(args)

	status, err := client.GetExchangeStatus(ctx)
	if err != nil {
		exitf("status failed: %v", err)
	}
	out, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(out))
}

func cmdMarkets(ctx context.Context, client *kalshi.Client, args []string) {
	fs := flag.NewFlagSet("markets", flag.ExitOnError)
	status := fs.String("status", "open", "market status filter")
	limit := fs.Int("limit", 20, "market result limit")
	cursor := fs.String("cursor", "", "pagination cursor")
	_ = fs.Parse(args)

	resp, err := client.GetMarkets(ctx, *status, *limit, *cursor)
	if err != nil {
		exitf("markets failed: %v", err)
	}
	for _, market := range resp.Markets {
		fmt.Printf("%s\t%s\t%s\tbid=%s\task=%s\tlast=%s\n",
			market.Ticker,
			market.Status,
			compact(market.Title, 90),
			emptyDash(market.YesBidDollars),
			emptyDash(market.YesAskDollars),
			emptyDash(market.LastPriceDollars),
		)
	}
	if resp.Cursor != "" {
		fmt.Printf("cursor=%s\n", resp.Cursor)
	}
}

func cmdOrderbook(ctx context.Context, client *kalshi.Client, args []string) {
	fs := flag.NewFlagSet("orderbook", flag.ExitOnError)
	ticker := fs.String("ticker", "", "market ticker")
	_ = fs.Parse(args)
	if strings.TrimSpace(*ticker) == "" {
		exitf("--ticker is required")
	}

	book, err := client.GetOrderbook(ctx, *ticker)
	if err != nil {
		exitf("orderbook failed: %v", err)
	}
	out, _ := json.MarshalIndent(book, "", "  ")
	fmt.Println(string(out))
}

func cmdOrders(ctx context.Context, client *kalshi.Client, args []string) {
	fs := flag.NewFlagSet("orders", flag.ExitOnError)
	status := fs.String("status", "resting", "order status filter")
	ticker := fs.String("ticker", "", "market ticker filter")
	eventTicker := fs.String("event-ticker", "", "event ticker filter")
	limit := fs.Int("limit", 50, "order result limit")
	cursor := fs.String("cursor", "", "pagination cursor")
	jsonOut := fs.Bool("json", false, "print raw JSON")
	_ = fs.Parse(args)

	resp, err := client.ListOrders(ctx, kalshi.OrderQuery{
		Ticker:      *ticker,
		EventTicker: *eventTicker,
		Status:      *status,
		Limit:       *limit,
		Cursor:      *cursor,
	})
	if err != nil {
		exitf("orders failed: %v", err)
	}
	if *jsonOut {
		out, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(out))
		return
	}
	for _, order := range resp.Orders {
		fmt.Printf("%s\t%s\t%s\t%s/%s\tstatus=%s\tyes=%s\tno=%s\tfilled=%s\tremaining=%s\tcreated=%s\n",
			emptyDash(order.OrderID),
			emptyDash(order.ClientOrderID),
			emptyDash(order.Ticker),
			emptyDash(order.Action),
			emptyDash(order.Side),
			emptyDash(order.Status),
			emptyDash(order.YesPriceDollars),
			emptyDash(order.NoPriceDollars),
			emptyDash(firstNonEmpty(order.FillCountFP, order.LegacyFillCount)),
			emptyDash(firstNonEmpty(order.RemainingCountFP, order.LegacyRemainingCount)),
			emptyDash(order.CreatedTime),
		)
	}
	if len(resp.Orders) == 0 {
		fmt.Println("No matching Kalshi orders.")
	}
	if resp.Cursor != "" {
		fmt.Printf("cursor=%s\n", resp.Cursor)
	}
}

func cmdCancelOrder(ctx context.Context, client *kalshi.Client, args []string) {
	fs := flag.NewFlagSet("cancel-order", flag.ExitOnError)
	orderID := fs.String("order-id", "", "Kalshi order id")
	_ = fs.Parse(args)
	if strings.TrimSpace(*orderID) == "" {
		exitf("--order-id is required")
	}

	resp, err := client.CancelOrder(ctx, *orderID)
	if err != nil {
		exitf("cancel-order failed: %v", err)
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(out))
}

func cmdPositions(ctx context.Context, client *kalshi.Client, args []string) {
	fs := flag.NewFlagSet("positions", flag.ExitOnError)
	ticker := fs.String("ticker", "", "market ticker filter")
	eventTicker := fs.String("event-ticker", "", "event ticker filter")
	countFilter := fs.String("count-filter", "", "optional Kalshi count_filter")
	limit := fs.Int("limit", 50, "position result limit")
	cursor := fs.String("cursor", "", "pagination cursor")
	jsonOut := fs.Bool("json", false, "print raw JSON")
	_ = fs.Parse(args)

	resp, err := client.GetPositions(ctx, kalshi.PositionQuery{
		Ticker:      *ticker,
		EventTicker: *eventTicker,
		CountFilter: *countFilter,
		Limit:       *limit,
		Cursor:      *cursor,
	})
	if err != nil {
		exitf("positions failed: %v", err)
	}
	if *jsonOut {
		out, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(out))
		return
	}
	for _, pos := range resp.MarketPositions {
		fmt.Printf("%s\tposition=%s\texposure=%s\tresting_orders=%d\trealized_pnl=%s\tfees=%s\n",
			emptyDash(pos.Ticker),
			emptyDash(pos.PositionFP),
			emptyDash(pos.MarketExposureDollars),
			pos.RestingOrdersCount,
			emptyDash(pos.RealizedPNLDollars),
			emptyDash(pos.FeesPaidDollars),
		)
	}
	if len(resp.MarketPositions) == 0 {
		fmt.Println("No matching Kalshi market positions.")
	}
	if len(resp.EventPositions) > 0 {
		fmt.Println("event_positions:")
		for _, pos := range resp.EventPositions {
			fmt.Printf("%s\texposure=%s\trealized_pnl=%s\tfees=%s\n",
				emptyDash(pos.EventTicker),
				emptyDash(pos.EventExposureDollars),
				emptyDash(pos.RealizedPNLDollars),
				emptyDash(pos.FeesPaidDollars),
			)
		}
	}
	if resp.Cursor != "" {
		fmt.Printf("cursor=%s\n", resp.Cursor)
	}
}

func cmdRestingValue(ctx context.Context, client *kalshi.Client, args []string) {
	fs := flag.NewFlagSet("resting-value", flag.ExitOnError)
	_ = fs.Parse(args)

	value, err := client.GetTotalRestingOrderValue(ctx)
	if err != nil {
		exitf("resting-value failed: %v", err)
	}
	fmt.Printf("total_resting_order_value=%s\n", kalshi.FormatCents(value.TotalRestingOrderValue))
}

func cmdOrder(ctx context.Context, client *kalshi.Client, args []string, place bool) {
	fs := flag.NewFlagSet("order", flag.ExitOnError)
	ticker := fs.String("ticker", "", "market ticker")
	side := fs.String("side", "yes", "yes or no")
	action := fs.String("action", "buy", "buy or sell")
	count := fs.Int64("count", 1, "whole contract count")
	price := fs.String("price", "", "fixed-point dollar price, e.g. 0.1200")
	tif := fs.String("tif", "fill_or_kill", "fill_or_kill, immediate_or_cancel, or good_till_canceled")
	clientOrderID := fs.String("client-order-id", "", "client order id")
	liveConfirm := fs.String("live-confirm", "", "must equal REAL_KALSHI_MONEY for real orders")
	postOnly := fs.Bool("post-only", false, "post only")
	reduceOnly := fs.Bool("reduce-only", false, "reduce only")
	_ = fs.Parse(args)

	if strings.TrimSpace(*price) == "" {
		exitf("--price is required")
	}
	id := strings.TrimSpace(*clientOrderID)
	if id == "" {
		id = "tf-" + uuid.NewString()
	}
	req := kalshi.OrderRequest{
		Ticker:                  *ticker,
		Side:                    *side,
		Action:                  *action,
		ClientOrderID:           id,
		Count:                   *count,
		TimeInForce:             *tif,
		SelfTradePreventionType: "taker_at_cross",
		CancelOrderOnPause:      true,
		ReduceOnly:              *reduceOnly,
	}
	if strings.EqualFold(strings.TrimSpace(*side), "no") {
		req.NoPriceDollars = *price
	} else {
		req.YesPriceDollars = *price
	}
	if *postOnly {
		req.PostOnly = postOnly
	}

	validation, err := client.ValidateOrder(req)
	if err != nil {
		exitf("order validation failed: %v", err)
	}
	fmt.Printf("estimated_max_risk=%s max_order=%s client_order_id=%s\n",
		kalshi.FormatCents(validation.EstimatedRiskCents),
		kalshi.FormatCents(validation.MaxOrderCents),
		id,
	)

	if !place {
		return
	}
	if *liveConfirm != liveConfirmation {
		exitf("real Kalshi order blocked; pass --live-confirm %s", liveConfirmation)
	}
	client.SetLiveConfirmation(*liveConfirm)

	resp, err := client.CreateOrder(ctx, req)
	if err != nil {
		exitf("order failed: %v", err)
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(out))
}

func compact(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 3 || len(value) <= limit {
		return value
	}
	return value[:limit-3] + "..."
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
