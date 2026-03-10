package ibkr_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

func requireGateway(t *testing.T) {
	t.Helper()
	if os.Getenv("IBKR_TEST") == "" {
		t.Skip("Set IBKR_TEST=1 to run IBKR integration tests")
	}
}

func requireOrderPermission(t *testing.T) {
	t.Helper()
	if os.Getenv("IBKR_TEST_ORDER") == "" {
		t.Skip("Set IBKR_TEST_ORDER=1 to run paper order placement test")
	}
}

func newClient(clientID int) *ibkr.Client {
	cfg := ibkr.DefaultConfig()
	cfg.ClientID = clientID
	return ibkr.NewClient(cfg)
}

func TestConnect(t *testing.T) {
	requireGateway(t)

	client := newClient(99)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if !client.IsConnected() {
		t.Fatal("expected connected client")
	}
	if !client.IsPaper() {
		t.Fatal("expected paper trading mode")
	}
}

func TestGetAccountSummary(t *testing.T) {
	requireGateway(t)

	client := newClient(98)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	summary, err := client.GetAccountSummary(context.Background())
	if err != nil {
		t.Fatalf("GetAccountSummary failed: %v", err)
	}
	if summary.NetLiquidation <= 0 {
		t.Fatalf("expected positive net liquidation, got %f", summary.NetLiquidation)
	}
}

func TestGetPositions(t *testing.T) {
	requireGateway(t)

	client := newClient(97)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if _, err := client.GetPositions(context.Background()); err != nil {
		t.Fatalf("GetPositions failed: %v", err)
	}
}

func TestReqMarketData(t *testing.T) {
	requireGateway(t)

	client := newClient(96)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	md, err := client.ReqMarketData(context.Background(), model.Instrument{
		Symbol:   "AAPL",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
	})
	if err != nil {
		t.Fatalf("ReqMarketData failed: %v", err)
	}
	if md.Last <= 0 && (md.Bid <= 0 || md.Ask <= 0) {
		t.Fatalf("expected a usable quote, got %+v", md)
	}
}

func TestPlacePaperOrder(t *testing.T) {
	requireGateway(t)
	requireOrderPermission(t)

	client := newClient(95)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	md, err := client.ReqMarketData(context.Background(), model.Instrument{
		Symbol:   "AAPL",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
	})
	if err != nil {
		t.Fatalf("ReqMarketData failed: %v", err)
	}

	price := md.Last
	if price <= 0 && md.Bid > 0 && md.Ask > 0 {
		price = (md.Bid + md.Ask) / 2
	}
	if price <= 0 {
		t.Fatalf("no usable price for order test: %+v", md)
	}

	fill, err := client.PlaceOrder(context.Background(), model.Order{
		ID: "integration-paper-order",
		Instrument: model.Instrument{
			Symbol:   "AAPL",
			SecType:  "STK",
			Exchange: "SMART",
			Currency: "USD",
		},
		Direction:   model.Long,
		Quantity:    1,
		OrderType:   model.OrderMarket,
		TimeInForce: "DAY",
		Notional:    price,
	})
	if err != nil {
		t.Fatalf("PlaceOrder failed: %v", err)
	}
	if fill.Quantity <= 0 {
		t.Fatalf("expected positive fill quantity, got %+v", fill)
	}
}
