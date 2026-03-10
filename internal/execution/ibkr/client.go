package ibkr

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/scmhub/ibapi"
	"github.com/scmhub/ibsync"

	"github.com/hnic/trading-floor/pkg/model"
)

// Client is the main IBKR API client wrapping ibsync.
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

type IBKRPosition struct {
	ConID    int64
	Symbol   string
	SecType  string
	Exchange string
	Currency string
	Quantity float64
	AvgCost  float64
}

type AccountSummary struct {
	NetLiquidation float64
	BuyingPower    float64
	Cash           float64
	UnrealizedPnL  float64
	RealizedPnL    float64
}

func NewClient(cfg Config) *Client {
	return &Client{
		conn: NewConnection(cfg),
		log:  slog.Default().With("component", "ibkr-client"),
	}
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

func (c *Client) Close() {
	c.conn.Disconnect()
}

func BuildContract(inst model.Instrument) *ibsync.Contract {
	contract := &ibsync.Contract{
		ConID:    inst.ConID,
		Symbol:   inst.Symbol,
		SecType:  inst.SecType,
		Exchange: inst.Exchange,
		Currency: inst.Currency,
		Strike:   inst.Strike,
		Right:    inst.Right,
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
	if inst.Multiplier != "" {
		contract.Multiplier = inst.Multiplier
	}
	return contract
}

func (c *Client) PlaceOrder(ctx context.Context, order model.Order) (*model.Fill, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	contract, err := c.qualifyContract(order.Instrument)
	if err != nil {
		return nil, err
	}

	ibOrder, err := buildOrder(order)
	if err != nil {
		return nil, err
	}

	c.log.Info("placing order",
		"symbol", order.Instrument.Symbol,
		"direction", order.Direction,
		"quantity", order.Quantity,
		"type", order.OrderType,
		"paper", c.IsPaper(),
	)

	trade := ib.PlaceOrder(contract, ibOrder)
	if trade == nil {
		return nil, fmt.Errorf("place order returned nil trade")
	}

	waitCtx, cancel := withDefaultTimeout(ctx, 60*time.Second)
	defer cancel()

	select {
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	case <-trade.Done():
	}

	if !trade.OrderStatus.IsDone() {
		return nil, fmt.Errorf("order did not complete")
	}

	fills := trade.Fills()
	if len(fills) == 0 {
		return nil, fmt.Errorf("order completed without fills")
	}

	totalQty := 0.0
	totalCost := 0.0
	totalCommission := 0.0
	filledAt := time.Now()

	for _, fill := range fills {
		if fill == nil || fill.Execution == nil {
			continue
		}
		qty := fill.Execution.Shares.Float()
		totalQty += qty
		totalCost += qty * fill.Execution.Price
		totalCommission += fill.CommissionAndFeesReport.CommissionAndFees
		if !fill.Time.IsZero() {
			filledAt = fill.Time
		}
	}

	if totalQty <= 0 {
		return nil, fmt.Errorf("order filled with non-positive quantity")
	}

	avgPrice := totalCost / totalQty
	instrument := order.Instrument
	instrument.ConID = contract.ConID
	if contract.Multiplier != "" {
		instrument.Multiplier = contract.Multiplier
	}

	return &model.Fill{
		OrderID:     order.ID,
		IBKROrderID: trade.Order.OrderID,
		Instrument:  instrument,
		Direction:   order.Direction,
		Quantity:    totalQty,
		AvgPrice:    avgPrice,
		Commission:  totalCommission,
		FilledAt:    filledAt,
	}, nil
}

func (c *Client) CancelOrder(ctx context.Context, orderID int64) error {
	ib := c.conn.IB()
	if ib == nil {
		return fmt.Errorf("not connected to IBKR")
	}

	for _, trade := range ib.OpenTrades() {
		if trade != nil && trade.Order != nil && trade.Order.OrderID == orderID {
			ib.CancelOrder(trade.Order, ibapi.NewOrderCancel())
			return nil
		}
	}

	return fmt.Errorf("order %d not found", orderID)
}

func (c *Client) GetPositions(ctx context.Context) ([]IBKRPosition, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	positions := ib.Positions()
	result := make([]IBKRPosition, 0, len(positions))
	for _, pos := range positions {
		if pos.Contract == nil {
			continue
		}
		result = append(result, IBKRPosition{
			ConID:    pos.Contract.ConID,
			Symbol:   pos.Contract.Symbol,
			SecType:  pos.Contract.SecType,
			Exchange: pos.Contract.Exchange,
			Currency: pos.Contract.Currency,
			Quantity: pos.Position.Float(),
			AvgCost:  pos.AvgCost,
		})
	}
	return result, nil
}

func (c *Client) GetAccountSummary(ctx context.Context) (*AccountSummary, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	summary := &AccountSummary{}
	for _, item := range ib.AccountSummary() {
		switch item.Tag {
		case "NetLiquidation":
			summary.NetLiquidation = parseFloat(item.Value)
		case "BuyingPower":
			summary.BuyingPower = parseFloat(item.Value)
		case "TotalCashValue":
			summary.Cash = parseFloat(item.Value)
		case "UnrealizedPnL":
			summary.UnrealizedPnL = parseFloat(item.Value)
		case "RealizedPnL":
			summary.RealizedPnL = parseFloat(item.Value)
		}
	}
	return summary, nil
}

func (c *Client) ReqMarketData(ctx context.Context, inst model.Instrument) (*MarketData, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	contract, err := c.qualifyContract(inst)
	if err != nil {
		return nil, err
	}

	waitCtx, cancel := withDefaultTimeout(ctx, 10*time.Second)
	defer cancel()

	ticker, err := ib.Snapshot(contract)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", inst.Symbol, err)
	}

	last := 0.0
	bid := 0.0
	ask := 0.0
	volume := int64(0)

	for {
		last = ticker.Last()
		bid = ticker.Bid()
		ask = ticker.Ask()
		volume = int64(math.Round(ticker.Volume().Float()))
		if last > 0 || (bid > 0 && ask > 0) {
			break
		}

		select {
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}

	return &MarketData{
		ConID:     contract.ConID,
		Symbol:    inst.Symbol,
		Last:      last,
		Bid:       bid,
		Ask:       ask,
		Volume:    volume,
		Timestamp: time.Now().UnixMilli(),
	}, nil
}

func (c *Client) qualifyContract(inst model.Instrument) (*ibsync.Contract, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	contract := BuildContract(inst)
	if err := ib.QualifyContract(contract); err != nil {
		return nil, fmt.Errorf("qualify contract %s: %w", inst.Symbol, err)
	}

	return contract, nil
}

func buildOrder(order model.Order) (*ibapi.Order, error) {
	action := "BUY"
	if order.Direction == model.Short {
		action = "SELL"
	}

	qty := normalizeQuantity(order.Quantity, order.Instrument.SecType)
	if qty <= 0 {
		return nil, fmt.Errorf("invalid quantity %.4f", order.Quantity)
	}

	decimalQty := ibapi.StringToDecimal(formatQuantity(qty))

	var ibOrder *ibapi.Order
	switch order.OrderType {
	case model.OrderMarket:
		ibOrder = ibapi.MarketOrder(action, decimalQty)
	case model.OrderLimit, model.OrderAdaptive, model.OrderTWAP:
		ibOrder = ibapi.LimitOrder(action, decimalQty, order.LimitPrice)
	case model.OrderStop:
		ibOrder = ibapi.Stop(action, decimalQty, order.StopPrice)
	case model.OrderStopLmt:
		ibOrder = ibapi.StopLimit(action, decimalQty, order.LimitPrice, order.StopPrice)
	case model.OrderMidPrice:
		ibOrder = ibapi.Midprice(action, decimalQty, order.LimitPrice)
	default:
		return nil, fmt.Errorf("unsupported order type %q", order.OrderType)
	}

	if order.TimeInForce != "" {
		ibOrder.TIF = order.TimeInForce
	}

	switch order.OrderType {
	case model.OrderAdaptive:
		ibOrder.AlgoStrategy = "Adaptive"
	case model.OrderTWAP:
		ibOrder.AlgoStrategy = "Twap"
	}

	return ibOrder, nil
}

func normalizeQuantity(quantity float64, secType string) float64 {
	switch secType {
	case "OPT", "FUT":
		return math.Round(quantity)
	default:
		return quantity
	}
}

func formatQuantity(quantity float64) string {
	return strconv.FormatFloat(quantity, 'f', 6, 64)
}

func parseFloat(value string) float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func withDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
