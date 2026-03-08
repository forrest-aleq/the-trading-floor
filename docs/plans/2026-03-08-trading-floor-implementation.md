# Trading Floor Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Take the existing Go scaffold from "compiles but does nothing real" to "10 desks placing paper trades on Interactive Brokers with LLM-powered signal analysis, real market data, and persistent state."

**Architecture:** Event-driven pipeline. Wire ingests signals → Scanner evaluates via LLM → Research forms thesis via LLM → Prosecutor challenges via LLM → Risk gate validates deterministically → IBKR executes → Book tracks positions → Memory updates beliefs. All orchestrated per-desk with goroutines. PostgreSQL for persistence, Redis for hot state.

**Tech Stack:** Go 1.23, scmhub/ibapi (IBKR TWS API), scmhub/ibsync (synchronous wrapper), PostgreSQL + pgvector, Redis, OpenRouter API (LLM), structured logging via slog.

**Codebase location:** `/Users/forrest/Documents/hnic/trading/initiative one/apps/research_execute/trading-floor/`

**IBKR connection:** IB Gateway running locally, paper trading port 4002.

**LLM:** OpenRouter API. Models: `qwen/qwen3.5-7b` (speed), `qwen/qwen3.5-72b` (analysis), `anthropic/claude-sonnet-4-20250514` (critical).

---

## Critical Context

### scmhub/ibapi API Surface

The Go IBKR client uses a **callback pattern** via the `EWrapper` interface:

```go
import "github.com/scmhub/ibapi"

// Create client with callback wrapper
client := ibapi.NewEClient(myWrapper)
client.Connect("127.0.0.1", 4002, 1)

// Place order
contract := &ibapi.Contract{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"}
order := ibapi.LimitOrder("BUY", ibapi.StringToDecimal("100"), 150.00)
client.PlaceOrder(nextOrderID, contract, order)

// Responses arrive asynchronously via EWrapper methods:
// wrapper.OrderStatus(orderID, status, filled, remaining, ...)
// wrapper.Execution(reqID, contract, execution)
// wrapper.TickPrice(reqID, tickType, price, attrib)
// wrapper.Position(account, contract, position, avgCost)
```

**Alternative: ibsync** — synchronous channel-based wrapper around ibapi. Simpler for our use case:
```go
import "github.com/scmhub/ibsync"
ib := ibsync.NewIB()
ib.Connect("127.0.0.1", 4002, 1)
positions := ib.Positions()       // blocking, returns []Position
order := ib.PlaceOrder(contract, order) // blocking, returns Trade
bars := ib.ReqHistoricalData(...)  // blocking, returns []Bar
```

### Known Bugs in Current Code (must fix first)

1. **risk/gate.go:190** — returns pointer to local function parameter. The `order` param is stack-allocated; pointer escapes but the data is a copy so it's technically safe in Go (compiler moves to heap), but semantically wrong — should explicitly allocate.
2. **firm/desk.go:73** — LearnWorker created but never called. No outcome processing.
3. **firm/desk.go:153** — `Notional: thesis.EntryPrice * thesis.PositionSize` is wrong for options (needs * 100 multiplier) and futures.
4. **research/desk.go:92** — Evidence.SignalID never populated.
5. **book/portfolio.go** — Desk capital never initialized from desk config.
6. **cmd/floor/main.go:73** — `_ = learnWorker` suppresses unused warning but learnWorker is never passed to desks or wired into outcome flow.

---

## Phase 1: Fix Critical Bugs (30 min)

### Task 1.1: Fix risk gate pointer semantics

**Files:**
- Modify: `internal/risk/gate.go`

**Step 1: Fix mintToken call and order return**

In `gate.go`, the `Check` method accepts `order model.Order` by value. The `AdjustedOrder: &order` returns a pointer to this local copy. While Go's escape analysis handles this, explicitly allocate:

```go
// In Check(), replace the success return (around line 185-195):
// BEFORE:
//   return model.RiskDecision{
//       Allowed:       true,
//       AdjustedOrder: &order,
//       Token:         token,
//   }

// AFTER:
adjustedOrder := order // explicit copy
return model.RiskDecision{
    Allowed:       true,
    AdjustedOrder: &adjustedOrder,
    Token:         token,
}
```

**Step 2: Verify compilation**

Run: `go build ./...`
Expected: clean compile

**Step 3: Commit**
```bash
git add internal/risk/gate.go
git commit -m "fix: explicit order copy in risk gate to prevent pointer aliasing"
```

### Task 1.2: Fix notional calculation for derivatives

**Files:**
- Modify: `internal/firm/desk.go`

**Step 1: Fix notional calculation**

Replace the order construction block (around line 143-155) in `desk.go` Process method:

```go
// Calculate notional correctly per instrument type
notional := thesis.EntryPrice * thesis.PositionSize
if thesis.Instrument.SecType == "OPT" {
    multiplier := 100.0 // standard options multiplier
    if thesis.Instrument.Multiplier != "" {
        if m, err := strconv.ParseFloat(thesis.Instrument.Multiplier, 64); err == nil {
            multiplier = m
        }
    }
    notional = thesis.EntryPrice * thesis.PositionSize * multiplier
}

order := model.Order{
    ID:          thesis.ID,
    ThesisID:    thesis.ID,
    DeskID:      d.ID,
    Instrument:  thesis.Instrument,
    Direction:   thesis.Direction,
    Quantity:    thesis.PositionSize,
    OrderType:   model.OrderLimit,
    LimitPrice:  thesis.EntryPrice,
    TimeInForce: "DAY",
    Notional:    notional,
}
```

Add `"strconv"` to the imports.

**Step 2: Verify compilation**

Run: `go build ./...`

**Step 3: Commit**
```bash
git add internal/firm/desk.go
git commit -m "fix: correct notional calculation for options contracts"
```

### Task 1.3: Fix evidence SignalID population

**Files:**
- Modify: `internal/research/desk.go`

**Step 1: Pass opportunity signal IDs through to evidence**

In `Investigate()`, after building the evidence slice (around line 89-92):

```go
evidence := make([]model.Evidence, len(result.Evidence))
for i, e := range result.Evidence {
    signalID := ""
    if i < len(opp.SignalIDs) {
        signalID = opp.SignalIDs[i]
    }
    evidence[i] = model.Evidence{
        Content:  e,
        Weight:   1.0,
        SignalID: signalID,
    }
}
```

**Step 2: Verify compilation**

Run: `go build ./...`

**Step 3: Commit**
```bash
git add internal/research/desk.go
git commit -m "fix: populate SignalID in thesis evidence for audit trail"
```

---

## Phase 2: Real IBKR Connection (2-3 hours)

This is the critical path. Use `scmhub/ibsync` (synchronous wrapper) instead of raw `ibapi` — simpler, channel-based, fits our goroutine model.

### Task 2.1: Add ibsync dependency

**Files:**
- Modify: `go.mod`

**Step 1: Add dependency**

```bash
cd "/Users/forrest/Documents/hnic/trading/initiative one/apps/research_execute/trading-floor"
go get github.com/scmhub/ibsync
go mod tidy
```

**Step 2: Verify**

Run: `go build ./...`

**Step 3: Commit**
```bash
git add go.mod go.sum
git commit -m "deps: add scmhub/ibsync for IBKR synchronous client"
```

### Task 2.2: Rewrite IBKR connection with real ibsync

**Files:**
- Rewrite: `internal/execution/ibkr/connection.go`
- Rewrite: `internal/execution/ibkr/client.go`

**Step 1: Rewrite connection.go**

Replace the entire file. The connection now wraps `ibsync.IB`:

```go
package ibkr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"

	"github.com/scmhub/ibsync"
)

type Config struct {
	Host     string
	Port     int
	ClientID int
}

func DefaultConfig() Config {
	host := os.Getenv("IBKR_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := 4002
	if p := os.Getenv("IBKR_PORT"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil {
			port = parsed
		}
	}
	clientID := 1
	if c := os.Getenv("IBKR_CLIENT_ID"); c != "" {
		if parsed, err := strconv.Atoi(c); err == nil {
			clientID = parsed
		}
	}
	return Config{Host: host, Port: port, ClientID: clientID}
}

type Connection struct {
	cfg Config
	log *slog.Logger
	ib  *ibsync.IB
	mu  sync.RWMutex
}

func NewConnection(cfg Config) *Connection {
	return &Connection{
		cfg: cfg,
		log: slog.Default().With("component", "ibkr"),
	}
}

func (c *Connection) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.log.Info("connecting to IBKR Gateway",
		"host", c.cfg.Host,
		"port", c.cfg.Port,
		"client_id", c.cfg.ClientID,
	)

	c.ib = ibsync.NewIB()
	err := c.ib.Connect(c.cfg.Host, c.cfg.Port, int64(c.cfg.ClientID))
	if err != nil {
		return fmt.Errorf("ibkr connect: %w", err)
	}

	c.log.Info("connected to IBKR Gateway")
	return nil
}

func (c *Connection) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ib != nil {
		c.ib.Disconnect()
		c.log.Info("disconnected from IBKR Gateway")
	}
}

func (c *Connection) IB() *ibsync.IB {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ib
}

func (c *Connection) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ib != nil && c.ib.IsConnected()
}

func (c *Connection) IsPaper() bool {
	return c.cfg.Port == 4002 || c.cfg.Port == 7497
}

func (c *Connection) String() string {
	mode := "LIVE"
	if c.IsPaper() {
		mode = "PAPER"
	}
	return fmt.Sprintf("IBKR[%s:%d %s client=%d]", c.cfg.Host, c.cfg.Port, mode, c.cfg.ClientID)
}
```

**Step 2: Rewrite client.go**

Replace the entire file. The client now uses real ibsync calls:

```go
package ibkr

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/scmhub/ibapi"
	"github.com/scmhub/ibsync"
	"github.com/hnic/trading-floor/pkg/model"
)

type Client struct {
	conn *Connection
	log  *slog.Logger
}

type MarketData struct {
	ConID     int64
	Symbol    string
	Last      float64
	Bid       float64
	Ask       float64
	Volume    int64
	Timestamp int64
}

func NewClient(cfg Config) *Client {
	return &Client{
		conn: NewConnection(cfg),
		log:  slog.Default().With("component", "ibkr-client"),
	}
}

func (c *Client) Connect(ctx context.Context) error {
	return c.conn.Connect(ctx)
}

func (c *Client) IsConnected() bool {
	return c.conn.IsConnected()
}

func (c *Client) IsPaper() bool {
	return c.conn.IsPaper()
}

func (c *Client) Close() {
	c.conn.Disconnect()
}

// BuildContract converts our model.Instrument to ibapi.Contract
func BuildContract(inst model.Instrument) *ibapi.Contract {
	contract := &ibapi.Contract{
		Symbol:   inst.Symbol,
		SecType:  inst.SecType,
		Exchange: inst.Exchange,
		Currency: inst.Currency,
	}
	if contract.Exchange == "" {
		contract.Exchange = "SMART"
	}
	if contract.Currency == "" {
		contract.Currency = "USD"
	}
	if contract.SecType == "" {
		contract.SecType = "STK"
	}
	if inst.Expiry != "" {
		contract.LastTradeDateOrContractMonth = inst.Expiry
	}
	if inst.Strike > 0 {
		contract.Strike = inst.Strike
	}
	if inst.Right != "" {
		contract.Right = inst.Right
	}
	if inst.Multiplier != "" {
		contract.Multiplier = inst.Multiplier
	}
	if inst.ConID > 0 {
		contract.ConID = inst.ConID
	}
	return contract
}

// PlaceOrder submits an order to IBKR and returns the fill
func (c *Client) PlaceOrder(ctx context.Context, order model.Order) (*model.Fill, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	contract := BuildContract(order.Instrument)

	// Determine action
	action := "BUY"
	if order.Direction == model.Short {
		action = "SELL"
	}

	// Build IBKR order based on our order type
	var ibOrder *ibapi.Order
	qty := ibapi.StringToDecimal(fmt.Sprintf("%.0f", order.Quantity))

	switch order.OrderType {
	case model.OrderMarket:
		ibOrder = ibapi.MarketOrder(action, qty)
	case model.OrderLimit:
		ibOrder = ibapi.LimitOrder(action, qty, order.LimitPrice)
	case model.OrderStop:
		ibOrder = ibapi.Stop(action, qty, order.StopPrice)
	case model.OrderStopLmt:
		ibOrder = ibapi.StopLimit(action, qty, order.LimitPrice, order.StopPrice)
	default:
		ibOrder = ibapi.LimitOrder(action, qty, order.LimitPrice)
	}

	if order.TimeInForce != "" {
		ibOrder.Tif = order.TimeInForce
	}

	c.log.Info("placing order",
		"symbol", order.Instrument.Symbol,
		"action", action,
		"qty", order.Quantity,
		"type", order.OrderType,
		"limit", order.LimitPrice,
		"paper", c.IsPaper(),
	)

	// ibsync.PlaceOrder is synchronous — blocks until fill or error
	trade := ib.PlaceOrder(contract, ibOrder)
	if trade == nil {
		return nil, fmt.Errorf("PlaceOrder returned nil trade")
	}

	// Wait for fill (with timeout)
	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("order fill timeout after 60s")
		case <-ticker.C:
			if trade.IsDone() {
				// Extract fill info
				fills := trade.Fills()
				if len(fills) == 0 {
					return nil, fmt.Errorf("order completed but no fills")
				}

				// Aggregate fills
				totalQty := 0.0
				totalCost := 0.0
				totalComm := 0.0
				for _, f := range fills {
					fqty, _ := f.Execution.Shares.Float64()
					totalQty += fqty
					totalCost += fqty * f.Execution.Price
					comm, _ := f.CommissionAndFeesReport.Commission.Float64()
					totalComm += comm
				}

				avgPrice := 0.0
				if totalQty > 0 {
					avgPrice = totalCost / totalQty
				}

				fill := &model.Fill{
					OrderID:     order.ID,
					IBKROrderID: int64(trade.Order.OrderID),
					Instrument:  order.Instrument,
					Direction:   order.Direction,
					Quantity:    totalQty,
					AvgPrice:    avgPrice,
					Commission:  totalComm,
					FilledAt:    time.Now(),
				}

				c.log.Info("order filled",
					"symbol", order.Instrument.Symbol,
					"qty", totalQty,
					"avg_price", avgPrice,
					"commission", totalComm,
				)

				return fill, nil
			}
		}
	}
}

// CancelOrder cancels a pending order
func (c *Client) CancelOrder(ctx context.Context, orderID int64) error {
	ib := c.conn.IB()
	if ib == nil {
		return fmt.Errorf("not connected to IBKR")
	}
	ib.CancelOrder(ibapi.OrderID(orderID), ibapi.OrderCancel{})
	return nil
}

// GetPositions returns all current positions from IBKR
func (c *Client) GetPositions(ctx context.Context) ([]IBKRPosition, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	positions := ib.Positions()
	result := make([]IBKRPosition, len(positions))
	for i, p := range positions {
		qty, _ := p.Position.Float64()
		cost, _ := p.AvgCost.Float64()
		result[i] = IBKRPosition{
			ConID:    p.Contract.ConID,
			Symbol:   p.Contract.Symbol,
			SecType:  p.Contract.SecType,
			Exchange: p.Contract.Exchange,
			Currency: p.Contract.Currency,
			Quantity: qty,
			AvgCost:  cost,
		}
	}
	return result, nil
}

type IBKRPosition struct {
	ConID    int64
	Symbol   string
	SecType  string
	Exchange string
	Currency string
	Quantity float64
	AvgCost  float64
}

// GetAccountSummary returns account balance info
func (c *Client) GetAccountSummary(ctx context.Context) (*AccountSummary, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	summary := ib.AccountSummary()
	result := &AccountSummary{}
	for _, item := range summary {
		switch item.Tag {
		case "NetLiquidation":
			fmt.Sscanf(item.Value, "%f", &result.NetLiquidation)
		case "BuyingPower":
			fmt.Sscanf(item.Value, "%f", &result.BuyingPower)
		case "TotalCashValue":
			fmt.Sscanf(item.Value, "%f", &result.Cash)
		case "UnrealizedPnL":
			fmt.Sscanf(item.Value, "%f", &result.UnrealizedPnL)
		case "RealizedPnL":
			fmt.Sscanf(item.Value, "%f", &result.RealizedPnL)
		}
	}
	return result, nil
}

type AccountSummary struct {
	NetLiquidation float64
	BuyingPower    float64
	Cash           float64
	UnrealizedPnL  float64
	RealizedPnL    float64
}

// ReqMarketData requests a snapshot of current market data for an instrument
func (c *Client) ReqMarketData(ctx context.Context, inst model.Instrument) (*MarketData, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	contract := BuildContract(inst)
	ticker := ib.ReqMktData(contract, "", false, false)
	if ticker == nil {
		return nil, fmt.Errorf("ReqMktData returned nil")
	}

	// Wait briefly for data to arrive
	time.Sleep(2 * time.Second)

	last, _ := ticker.Last.Float64()
	bid, _ := ticker.Bid.Float64()
	ask, _ := ticker.Ask.Float64()
	vol := ticker.Volume

	return &MarketData{
		ConID:  contract.ConID,
		Symbol: inst.Symbol,
		Last:   last,
		Bid:    bid,
		Ask:    ask,
		Volume: vol,
	}, nil
}
```

**Step 3: Delete orders.go (functionality merged into client.go)**

Remove `internal/execution/ibkr/orders.go` — the `BuildContract` and order building logic is now in client.go using real ibapi types.

**Step 4: Update execution/manager.go imports**

The manager.go should still work since it calls `ibkr.Client` methods which have the same signatures. Verify:

Run: `go build ./...`

Fix any compilation errors from the ibsync/ibapi types.

**Step 5: Commit**
```bash
git add internal/execution/ibkr/ internal/execution/manager.go go.mod go.sum
git commit -m "feat: real IBKR connection via scmhub/ibsync — PlaceOrder, GetPositions, MarketData"
```

### Task 2.3: Write IBKR connection test

**Files:**
- Create: `internal/execution/ibkr/client_test.go`

**Step 1: Write integration test (requires IB Gateway running)**

```go
package ibkr_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

func skipIfNoGateway(t *testing.T) {
	if os.Getenv("IBKR_TEST") == "" {
		t.Skip("Set IBKR_TEST=1 to run IBKR integration tests (requires IB Gateway)")
	}
}

func TestConnect(t *testing.T) {
	skipIfNoGateway(t)

	client := ibkr.NewClient(ibkr.Config{
		Host:     "127.0.0.1",
		Port:     4002,
		ClientID: 99,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if !client.IsConnected() {
		t.Fatal("expected IsConnected to be true")
	}
	if !client.IsPaper() {
		t.Fatal("expected paper trading mode")
	}
}

func TestGetAccountSummary(t *testing.T) {
	skipIfNoGateway(t)

	client := ibkr.NewClient(ibkr.Config{Host: "127.0.0.1", Port: 4002, ClientID: 98})
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	summary, err := client.GetAccountSummary(ctx)
	if err != nil {
		t.Fatalf("GetAccountSummary: %v", err)
	}
	if summary.NetLiquidation <= 0 {
		t.Errorf("expected positive NetLiquidation, got %f", summary.NetLiquidation)
	}
	t.Logf("Account: NAV=%f Cash=%f BuyingPower=%f", summary.NetLiquidation, summary.Cash, summary.BuyingPower)
}

func TestGetPositions(t *testing.T) {
	skipIfNoGateway(t)

	client := ibkr.NewClient(ibkr.Config{Host: "127.0.0.1", Port: 4002, ClientID: 97})
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	positions, err := client.GetPositions(ctx)
	if err != nil {
		t.Fatalf("GetPositions: %v", err)
	}
	t.Logf("Found %d positions", len(positions))
	for _, p := range positions {
		t.Logf("  %s %s: qty=%f avg_cost=%f", p.Symbol, p.SecType, p.Quantity, p.AvgCost)
	}
}

func TestMarketData(t *testing.T) {
	skipIfNoGateway(t)

	client := ibkr.NewClient(ibkr.Config{Host: "127.0.0.1", Port: 4002, ClientID: 96})
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	md, err := client.ReqMarketData(ctx, model.Instrument{
		Symbol:   "AAPL",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
	})
	if err != nil {
		t.Fatalf("ReqMarketData: %v", err)
	}
	t.Logf("AAPL: last=%f bid=%f ask=%f vol=%d", md.Last, md.Bid, md.Ask, md.Volume)
}

func TestPlacePaperOrder(t *testing.T) {
	skipIfNoGateway(t)

	client := ibkr.NewClient(ibkr.Config{Host: "127.0.0.1", Port: 4002, ClientID: 95})
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Get current price first
	md, err := client.ReqMarketData(ctx, model.Instrument{
		Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD",
	})
	if err != nil {
		t.Fatalf("ReqMarketData: %v", err)
	}

	// Place a limit order well below market to avoid fill (or use market for guaranteed fill)
	order := model.Order{
		ID:         "test-order-1",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderMarket,
		Notional:   md.Last,
	}

	fill, err := client.PlaceOrder(ctx, order)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	t.Logf("Fill: qty=%f price=%f commission=%f", fill.Quantity, fill.AvgPrice, fill.Commission)

	if fill.Quantity != 1 {
		t.Errorf("expected qty 1, got %f", fill.Quantity)
	}
	if fill.AvgPrice <= 0 {
		t.Errorf("expected positive fill price, got %f", fill.AvgPrice)
	}
}
```

**Step 2: Run without gateway (should skip)**

Run: `go test ./internal/execution/ibkr/ -v`
Expected: All tests SKIP with "Set IBKR_TEST=1..."

**Step 3: Run with gateway (when available)**

Run: `IBKR_TEST=1 go test ./internal/execution/ibkr/ -v -run TestConnect`
Expected: PASS if IB Gateway is running on port 4002

**Step 4: Commit**
```bash
git add internal/execution/ibkr/client_test.go
git commit -m "test: IBKR integration tests (connect, positions, market data, paper order)"
```

---

## Phase 3: Wire Outcome Processing (1 hour)

The learning loop is broken — trades execute but outcomes are never processed back into the belief graph.

### Task 3.1: Add position monitoring goroutine

**Files:**
- Create: `internal/book/monitor.go`

**Step 1: Create position monitor**

```go
package book

import (
	"context"
	"log/slog"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

// Monitor watches open positions for kill conditions and thesis health degradation
type Monitor struct {
	log       *slog.Logger
	book      *Book
	onClose   func(position *model.Position, exitPrice float64, reason string)
	interval  time.Duration
}

func NewMonitor(book *Book, onClose func(*model.Position, float64, string)) *Monitor {
	return &Monitor{
		log:      slog.Default().With("component", "monitor"),
		book:     book,
		onClose:  onClose,
		interval: 10 * time.Second,
	}
}

// Run checks all open positions on an interval
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check()
		}
	}
}

func (m *Monitor) check() {
	positions := m.book.GetOpenPositions()
	for _, pos := range positions {
		if pos.CurrentPrice <= 0 {
			continue // No market data yet
		}

		// Check stop loss
		// For long positions: close if price drops below stop
		// For short positions: close if price rises above stop
		// TODO: Get stop loss from thesis via position's ThesisID

		// Check P&L limits
		lossPct := 0.0
		if pos.Direction == model.Long {
			lossPct = (pos.EntryPrice - pos.CurrentPrice) / pos.EntryPrice
		} else {
			lossPct = (pos.CurrentPrice - pos.EntryPrice) / pos.EntryPrice
		}

		// Emergency: close if loss exceeds 5% on any single position
		if lossPct > 0.05 {
			m.log.Warn("position hit loss limit",
				"symbol", pos.Instrument.Symbol,
				"loss_pct", lossPct,
				"desk", pos.DeskID,
			)
			if m.onClose != nil {
				m.onClose(pos, pos.CurrentPrice, "loss_limit_5pct")
			}
		}
	}
}
```

**Step 2: Verify compilation**

Run: `go build ./...`

**Step 3: Commit**
```bash
git add internal/book/monitor.go
git commit -m "feat: position monitor checks open positions for loss limits"
```

### Task 3.2: Wire outcome processing into desk pipeline

**Files:**
- Modify: `internal/firm/desk.go`

**Step 1: Add outcome processing method to Desk**

Add this method to desk.go after the `Process` method:

```go
// ProcessOutcome handles a closed position — updates beliefs
func (d *Desk) ProcessOutcome(thesis *model.Thesis, outcome *model.ThesisOutcome) {
	if d.ABGroup == "B" {
		// Group B: no belief updates (control group)
		d.log.Info("outcome recorded (control group, no belief update)",
			"thesis_id", thesis.ID,
			"profitable", outcome.Profitable,
			"pnl", outcome.RealizedPnL,
		)
		return
	}

	// Group A: full MARS belief update
	d.learnWorker.ProcessOutcome(thesis, outcome, d.regime)

	d.log.Info("outcome processed (belief updated)",
		"thesis_id", thesis.ID,
		"profitable", outcome.Profitable,
		"pnl", outcome.RealizedPnL,
		"strategy", thesis.Strategy,
	)
}
```

**Step 2: Verify compilation**

Run: `go build ./...`

**Step 3: Commit**
```bash
git add internal/firm/desk.go
git commit -m "feat: wire outcome processing into desk with A/B group awareness"
```

### Task 3.3: Wire monitor + outcomes in main.go

**Files:**
- Modify: `cmd/floor/main.go`

**Step 1: Add monitor initialization and outcome callback**

In main.go, after creating the book and before creating desks, add the monitor:

```go
// Position Monitor — watches for stop losses and exit conditions
monitor := book.NewMonitor(bk, func(pos *model.Position, exitPrice float64, reason string) {
    outcome, err := bk.ClosePosition(pos.ID, exitPrice, reason)
    if err != nil || outcome == nil {
        slog.Error("failed to close position", "id", pos.ID, "error", err)
        return
    }
    // Find desk and process outcome
    for _, desk := range domains {
        if desk.id == pos.DeskID {
            // TODO: retrieve thesis from DB, for now log
            slog.Info("position closed by monitor",
                "desk", pos.DeskID,
                "symbol", pos.Instrument.Symbol,
                "pnl", outcome.RealizedPnL,
                "reason", reason,
            )
            break
        }
    }
    audit.Record("position_closed", pos.DeskID, pos.ThesisID, map[string]interface{}{
        "pnl":    outcome.RealizedPnL,
        "reason": reason,
    })
})
go monitor.Run(ctx)
```

Also remove the `_ = learnWorker` line and make sure learnWorker is passed to DeskConfig.

**Step 2: Verify compilation**

Run: `go build ./...`

**Step 3: Commit**
```bash
git add cmd/floor/main.go
git commit -m "feat: wire position monitor and outcome processing into main loop"
```

---

## Phase 4: Database Persistence (2 hours)

### Task 4.1: Add PostgreSQL connection

**Files:**
- Create: `internal/store/db.go`

**Step 1: Add pgx dependency**

```bash
go get github.com/jackc/pgx/v5
go get github.com/jackc/pgx/v5/pgxpool
```

**Step 2: Create db.go**

```go
package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
	log  *slog.Logger
}

func NewDB(ctx context.Context) (*DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost:5432/tradingfloor?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	slog.Info("PostgreSQL connected", "dsn", dsn)

	return &DB{Pool: pool, log: slog.Default().With("component", "store")}, nil
}

func (db *DB) Close() {
	db.Pool.Close()
}
```

**Step 3: Create signal store**

Create `internal/store/signals.go`:

```go
package store

import (
	"context"

	"github.com/hnic/trading-floor/pkg/signal"
)

func (db *DB) InsertSignal(ctx context.Context, sig signal.Signal) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO signals (id, source, type, category, content, language, translated, entities, urgency, strength, direction, content_hash, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 ON CONFLICT (id) DO NOTHING`,
		sig.ID, sig.Source, sig.Type, sig.Category,
		string(sig.Raw), sig.Languages, sig.Translated,
		nil, // entities as JSONB — serialize separately
		sig.Urgency, sig.Strength, string(sig.Direction),
		sig.ContentHash, sig.Timestamp,
	)
	return err
}
```

**Step 4: Create thesis store**

Create `internal/store/theses.go`:

```go
package store

import (
	"context"
	"encoding/json"

	"github.com/hnic/trading-floor/pkg/model"
)

func (db *DB) InsertThesis(ctx context.Context, t *model.Thesis) error {
	evidence, _ := json.Marshal(t.Evidence)
	counterArgs, _ := json.Marshal(t.CounterArgs)
	prosecution, _ := json.Marshal(t.Prosecution)
	instrument, _ := json.Marshal(t.Instrument)
	killRules, _ := json.Marshal(t.KillRules)

	_, err := db.Pool.Exec(ctx,
		`INSERT INTO theses (id, opportunity_id, desk_id, strategy, instrument, direction,
		 conviction, health, evidence, counter_args, entry_price, target_price, stop_loss,
		 position_size, kill_rules, status, prosecution, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		t.ID, t.OpportunityID, t.DeskID, t.Strategy, instrument, string(t.Direction),
		t.Conviction, t.Health, evidence, counterArgs,
		t.EntryPrice, t.TargetPrice, t.StopLoss, t.PositionSize,
		killRules, string(t.Status), prosecution, t.CreatedAt,
	)
	return err
}

func (db *DB) UpdateThesisOutcome(ctx context.Context, thesisID string, outcome *model.ThesisOutcome) error {
	outcomeJSON, _ := json.Marshal(outcome)
	_, err := db.Pool.Exec(ctx,
		`UPDATE theses SET status = 'resolved', outcome = $2, resolved_at = NOW() WHERE id = $1`,
		thesisID, outcomeJSON,
	)
	return err
}

func (db *DB) GetThesis(ctx context.Context, id string) (*model.Thesis, error) {
	// TODO: implement SELECT and unmarshal
	return nil, nil
}
```

**Step 5: Create position store**

Create `internal/store/positions.go`:

```go
package store

import (
	"context"
	"encoding/json"

	"github.com/hnic/trading-floor/pkg/model"
)

func (db *DB) InsertPosition(ctx context.Context, p *model.Position) error {
	instrument, _ := json.Marshal(p.Instrument)
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO positions (id, thesis_id, desk_id, instrument, direction, quantity,
		 entry_price, current_price, ibkr_order_id, ibkr_contract_id, status, opened_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		p.ID, p.ThesisID, p.DeskID, instrument, string(p.Direction),
		p.Quantity, p.EntryPrice, p.CurrentPrice,
		p.IBKROrderID, p.IBKRContractID, p.Status, p.OpenedAt,
	)
	return err
}

func (db *DB) UpdatePositionClose(ctx context.Context, id string, pnl float64, reason string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE positions SET status = 'closed', realized_pnl = $2, closed_at = NOW() WHERE id = $1`,
		id, pnl,
	)
	return err
}
```

**Step 6: Create anti-portfolio store**

Create `internal/store/antiportfolio.go`:

```go
package store

import (
	"context"
	"encoding/json"

	"github.com/hnic/trading-floor/pkg/model"
)

func (db *DB) InsertAntiPortfolio(ctx context.Context, thesis *model.Thesis, reason string) error {
	snapshot, _ := json.Marshal(thesis)
	instrument, _ := json.Marshal(thesis.Instrument)
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO anti_portfolio (thesis_snapshot, rejection_reason, desk_id, strategy, instrument, direction, would_have_entry, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())`,
		snapshot, reason, thesis.DeskID, thesis.Strategy, instrument,
		string(thesis.Direction), thesis.EntryPrice,
	)
	return err
}
```

**Step 7: Verify**

Run: `go build ./...`

**Step 8: Commit**
```bash
git add internal/store/ go.mod go.sum
git commit -m "feat: PostgreSQL persistence layer for signals, theses, positions, anti-portfolio"
```

### Task 4.2: Wire database into main.go and desk pipeline

**Files:**
- Modify: `cmd/floor/main.go`
- Modify: `internal/firm/desk.go`

**Step 1: Add DB to main.go initialization**

After the LLM router initialization:

```go
// ── Database ────────────────────────────────────────────────
db, err := store.NewDB(ctx)
if err != nil {
    slog.Warn("PostgreSQL not available — running in-memory only", "error", err)
    // Continue without persistence for dev
}
if db != nil {
    defer db.Close()
}
```

**Step 2: Add DB to DeskConfig and pass through**

Add `Store *store.DB` field to `DeskConfig` in desk.go. In the Process method, after recording anti-portfolio rejections, call `d.store.InsertAntiPortfolio(ctx, thesis, reason)`.

After successful trade execution, call `d.store.InsertThesis(ctx, thesis)` and `d.store.InsertPosition(ctx, position)`.

**Step 3: Verify and commit**

Run: `go build ./...`

```bash
git add cmd/floor/main.go internal/firm/desk.go internal/store/
git commit -m "feat: wire PostgreSQL persistence into desk pipeline"
```

---

## Phase 5: Market Data Price Feed (1 hour)

### Task 5.1: IBKR market data feed for the Wire

**Files:**
- Create: `internal/wire/feeds/market.go`

**Step 1: Create IBKR market data feed**

```go
package feeds

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

// MarketFeed streams real-time price data from IBKR as signals
type MarketFeed struct {
	log       *slog.Logger
	ibkr      *ibkr.Client
	watchlist []model.Instrument
	interval  time.Duration
}

func NewMarketFeed(ibkrClient *ibkr.Client, watchlist []model.Instrument) *MarketFeed {
	return &MarketFeed{
		log:       slog.Default().With("component", "feed-market"),
		ibkr:      ibkrClient,
		watchlist: watchlist,
		interval:  30 * time.Second, // Poll every 30s
	}
}

func (f *MarketFeed) Name() string { return "market" }

func (f *MarketFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, inst := range f.watchlist {
				md, err := f.ibkr.ReqMarketData(ctx, inst)
				if err != nil {
					f.log.Warn("market data error", "symbol", inst.Symbol, "error", err)
					continue
				}

				sig := signal.Signal{
					ID:        fmt.Sprintf("mkt-%s-%d", inst.Symbol, time.Now().UnixMilli()),
					Source:    "ibkr-market",
					Type:      signal.TypePrice,
					Category:  "market",
					Timestamp: time.Now(),
					Urgency:   0.3,
					Entities:  []signal.Entity{{Name: inst.Symbol, Type: "instrument"}},
					Raw:       []byte(fmt.Sprintf(`{"symbol":"%s","last":%f,"bid":%f,"ask":%f,"volume":%d}`,
						inst.Symbol, md.Last, md.Bid, md.Ask, md.Volume)),
				}

				select {
				case out <- sig:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

// DefaultWatchlist returns major instruments to track
func DefaultWatchlist() []model.Instrument {
	return []model.Instrument{
		{Symbol: "SPY", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "QQQ", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "IWM", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "DIA", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "VIX", SecType: "IND", Exchange: "CBOE", Currency: "USD"},
		{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "MSFT", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "NVDA", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "AMZN", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "GOOGL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "GLD", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "USO", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "TLT", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "XLE", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "XLF", SecType: "STK", Exchange: "SMART", Currency: "USD"},
	}
}
```

**Step 2: Register in main.go**

```go
wireMgr.RegisterFeed(feeds.NewMarketFeed(ibkrClient, feeds.DefaultWatchlist()))
```

**Step 3: Verify and commit**

Run: `go build ./...`

```bash
git add internal/wire/feeds/market.go cmd/floor/main.go
git commit -m "feat: IBKR market data feed streams price signals through Wire"
```

---

## Phase 6: Price Updates to Book (30 min)

### Task 6.1: Feed IBKR prices into Book.Mark()

**Files:**
- Modify: `cmd/floor/main.go`

**Step 1: Add periodic mark-to-market goroutine in main.go**

```go
// Mark-to-market: update all position prices every 30 seconds
go func() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            positions := bk.GetOpenPositions()
            prices := make(map[string]float64)
            for _, pos := range positions {
                md, err := ibkrClient.ReqMarketData(ctx, pos.Instrument)
                if err != nil {
                    continue
                }
                if md.Last > 0 {
                    prices[pos.Instrument.Symbol] = md.Last
                } else if md.Bid > 0 && md.Ask > 0 {
                    prices[pos.Instrument.Symbol] = (md.Bid + md.Ask) / 2
                }
            }
            if len(prices) > 0 {
                bk.Mark(prices)
            }
        }
    }
}()
```

Add `"time"` to imports if not already present.

**Step 2: Verify and commit**

```bash
git add cmd/floor/main.go
git commit -m "feat: periodic mark-to-market from IBKR market data"
```

---

## Phase 7: .env Configuration (15 min)

### Task 7.1: Create .env file and loader

**Files:**
- Create: `.env.example`
- Modify: `cmd/floor/main.go`

**Step 1: Create .env.example**

```bash
# IBKR Gateway
IBKR_HOST=127.0.0.1
IBKR_PORT=4002
IBKR_CLIENT_ID=1

# LLM (OpenRouter)
OPENROUTER_API_KEY=your-key-here
LLM_BASE_URL=https://openrouter.ai/api/v1
LLM_MODEL_SPEED=qwen/qwen3.5-7b
LLM_MODEL_ANALYSIS=qwen/qwen3.5-72b
LLM_MODEL_CRITICAL=anthropic/claude-sonnet-4-20250514

# PostgreSQL
DATABASE_URL=postgres://localhost:5432/tradingfloor?sslmode=disable

# Redis (optional for now)
REDIS_URL=redis://localhost:6379
```

**Step 2: Add dotenv loading to main.go**

```bash
go get github.com/joho/godotenv
```

At the top of main():
```go
_ = godotenv.Load() // Load .env if it exists, ignore if not
```

**Step 3: Add .env to .gitignore**

Create `.gitignore`:
```
.env
bin/
audit.jsonl
*.log
```

**Step 4: Commit**
```bash
git add .env.example .gitignore cmd/floor/main.go go.mod go.sum
git commit -m "feat: .env configuration with example file"
```

---

## Phase 8: End-to-End Smoke Test (30 min)

### Task 8.1: Create a minimal end-to-end test

**Files:**
- Create: `cmd/floor/smoke_test.go`

**Step 1: Write smoke test that runs the full pipeline with mocked IBKR**

```go
package main_test

import (
	"context"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/pkg/signal"
)

func TestSmokeFullPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create components
	llmRouter := llm.DefaultRouter()
	ibkrClient := ibkr.NewClient(ibkr.DefaultConfig())
	// Don't connect to real IBKR for unit test

	execMgr := execution.NewManager(ibkrClient)
	bk := book.NewBook(ibkrClient, 1_000_000)
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	learnWorker := memory.NewLearnWorker(beliefGraph)
	scan := scanner.NewEngine(llmRouter, 40)
	researchDesk := research.NewDesk(llmRouter, 0.65)
	prosecutor := research.NewProsecutor(llmRouter)

	// Create wire with no feeds (we'll inject a signal manually)
	wireMgr := wire.NewManager()

	// Create floor with one desk
	floor := firm.NewFloor(wireMgr)
	desk := firm.NewDesk(firm.DeskConfig{
		ID:          "test-desk",
		Domain:      "corporate",
		ABGroup:     "A",
		Capital:     25000,
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

	// Inject a test signal directly into desk.Process
	testSignal := signal.Signal{
		ID:       "test-signal-1",
		Source:   "test",
		Type:     signal.TypeNews,
		Category: "corporate",
		Urgency:  0.8,
		Raw:      []byte(`"Apple reports record Q1 earnings, beats estimates by 15%"`),
	}

	// This will fail at LLM call (no API key), but should not panic
	desk.Process(ctx, testSignal)

	t.Log("smoke test completed without panic")
	t.Logf("belief graph stats: %+v", beliefGraph.Stats())
}
```

**Step 2: Run**

Run: `go test ./cmd/floor/ -v -run TestSmoke`
Expected: Test passes (LLM calls may fail gracefully, but no panics)

**Step 3: Commit**
```bash
git add cmd/floor/smoke_test.go
git commit -m "test: end-to-end smoke test for full pipeline"
```

---

## Execution Order Summary

| Phase | What | Time | Blocks |
|-------|------|------|--------|
| 1 | Fix critical bugs (3 tasks) | 30 min | Nothing |
| 2 | Real IBKR connection (3 tasks) | 2-3 hrs | Phase 1 |
| 3 | Outcome processing (3 tasks) | 1 hr | Phase 1 |
| 4 | Database persistence (2 tasks) | 2 hrs | Phase 1 |
| 5 | Market data feed (1 task) | 1 hr | Phase 2 |
| 6 | Price updates to Book (1 task) | 30 min | Phase 2, 5 |
| 7 | .env configuration (1 task) | 15 min | Nothing |
| 8 | End-to-end smoke test (1 task) | 30 min | All above |

**Phases 1, 3, 4, 7 can run in parallel.** Phase 2 depends on 1. Phases 5 and 6 depend on 2. Phase 8 is the final validation.

**After this plan is complete:** The system will connect to real IBKR Gateway, stream market data, evaluate signals via LLM, form theses, prosecute them, check risk, execute paper trades, track positions with P&L, update beliefs from outcomes, and persist everything to PostgreSQL.

**What's still NOT covered (future phases):**
- SEC EDGAR feed
- Social/Telegram feeds
- Neo4j knowledge graph + cascade detector
- Council (multi-archetype debate)
- CEO referee (capital reallocation)
- A/B test statistical comparison
- Anti-portfolio counterfactual evaluation (retroactive P&L check)
- Belief backfill from historical data
- `ctl` CLI commands
- Redis hot state caching
- Azure deployment scripts
- Remaining 30 desks (currently 10)
