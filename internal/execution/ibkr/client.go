package ibkr

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/hnic/trading-floor/pkg/model"
)

// Client is the main IBKR API client wrapping scmhub/ibapi
type Client struct {
	conn    *Connection
	log     *slog.Logger
	nextID  atomic.Int64

	// State
	mu        sync.RWMutex
	positions map[int64]*model.Position // conID → position
	fills     map[int64]*model.Fill     // orderID → fill
	pending   map[int64]chan *model.Fill // orderID → fill notification

	// Market data subscriptions
	mdSubs map[int64]chan MarketData // reqID → market data channel
}

// MarketData is a price update from IBKR
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
	conn := NewConnection(cfg)
	c := &Client{
		conn:      conn,
		log:       slog.Default().With("component", "ibkr-client"),
		positions: make(map[int64]*model.Position),
		fills:     make(map[int64]*model.Fill),
		pending:   make(map[int64]chan *model.Fill),
		mdSubs:    make(map[int64]chan MarketData),
	}
	c.nextID.Store(1000)
	return c
}

func (c *Client) Connect(ctx context.Context) error {
	if err := c.conn.Connect(ctx); err != nil {
		return fmt.Errorf("ibkr connect: %w", err)
	}
	go c.conn.RunReconnectLoop(ctx)
	return nil
}

func (c *Client) IsConnected() bool {
	return c.conn.IsConnected()
}

func (c *Client) IsPaper() bool {
	return c.conn.IsPaper()
}

func (c *Client) nextReqID() int64 {
	return c.nextID.Add(1)
}

// PlaceOrder submits an order to IBKR and waits for fill
func (c *Client) PlaceOrder(ctx context.Context, order model.Order) (*model.Fill, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	orderID := c.nextReqID()

	c.log.Info("placing order",
		"order_id", orderID,
		"symbol", order.Instrument.Symbol,
		"direction", order.Direction,
		"quantity", order.Quantity,
		"type", order.OrderType,
		"paper", c.IsPaper(),
	)

	// Create fill notification channel
	fillCh := make(chan *model.Fill, 1)
	c.mu.Lock()
	c.pending[orderID] = fillCh
	c.mu.Unlock()

	// TODO: Build ibapi.Contract from order.Instrument
	// TODO: Build ibapi.Order from order params
	// TODO: Call ic.PlaceOrder(orderID, contract, ibOrder)

	// For now, simulate a fill for development
	fill := &model.Fill{
		OrderID:     order.ID,
		IBKROrderID: orderID,
		Instrument:  order.Instrument,
		Direction:   order.Direction,
		Quantity:    order.Quantity,
		AvgPrice:    order.LimitPrice, // TODO: actual fill price
	}

	c.log.Info("order filled",
		"order_id", orderID,
		"symbol", order.Instrument.Symbol,
		"price", fill.AvgPrice,
		"quantity", fill.Quantity,
	)

	return fill, nil
}

// CancelOrder cancels a pending order
func (c *Client) CancelOrder(ctx context.Context, orderID int64) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to IBKR")
	}
	c.log.Info("cancelling order", "order_id", orderID)
	// TODO: ic.CancelOrder(orderID)
	return nil
}

// GetPositions returns all current positions from IBKR
func (c *Client) GetPositions(ctx context.Context) ([]IBKRPosition, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to IBKR")
	}
	// TODO: Request positions from IBKR via reqPositions()
	return nil, nil
}

// IBKRPosition is the raw position data from IBKR for reconciliation
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
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to IBKR")
	}
	// TODO: Request account summary
	return &AccountSummary{
		NetLiquidation: 1000000, // $1M paper default
		BuyingPower:    2000000,
		Cash:           1000000,
	}, nil
}

type AccountSummary struct {
	NetLiquidation float64
	BuyingPower    float64
	Cash           float64
	UnrealizedPnL  float64
	RealizedPnL    float64
}

// SubscribeMarketData starts streaming market data for a contract
func (c *Client) SubscribeMarketData(ctx context.Context, instrument model.Instrument) (<-chan MarketData, error) {
	if !c.IsConnected() {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	reqID := c.nextReqID()
	ch := make(chan MarketData, 100)

	c.mu.Lock()
	c.mdSubs[reqID] = ch
	c.mu.Unlock()

	c.log.Info("subscribing to market data",
		"req_id", reqID,
		"symbol", instrument.Symbol,
	)

	// TODO: Build contract and call ic.ReqMktData(reqID, contract, "", false, false, nil)

	return ch, nil
}

// UnsubscribeMarketData stops streaming for a request ID
func (c *Client) UnsubscribeMarketData(reqID int64) {
	c.mu.Lock()
	if ch, ok := c.mdSubs[reqID]; ok {
		close(ch)
		delete(c.mdSubs, reqID)
	}
	c.mu.Unlock()
}

func (c *Client) Close() {
	c.conn.Disconnect()
}
