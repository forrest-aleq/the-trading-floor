package ibkr

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/scmhub/ibapi"
	"github.com/scmhub/ibsync"

	"github.com/hnic/trading-floor/pkg/model"
)

// Client is the main IBKR API client wrapping ibsync.
type Client struct {
	conn          connectionAPI
	log           *slog.Logger
	reconnectOnce sync.Once
}

type connectionAPI interface {
	Connect(context.Context) error
	Disconnect()
	IB() *ibsync.IB
	IsConnected() bool
	IsPaper() bool
	RunReconnectLoop(context.Context)
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
	c.startReconnectLoop(ctx)
	if err := c.conn.Connect(ctx); err != nil {
		return fmt.Errorf("ibkr connect: %w", err)
	}
	return nil
}

func (c *Client) startReconnectLoop(ctx context.Context) {
	c.reconnectOnce.Do(func() {
		go c.conn.RunReconnectLoop(ctx)
	})
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

	contract := (*ibsync.Contract)(nil)
	resolvedLegs := append([]model.TradeLeg(nil), order.Legs...)
	var err error
	if order.IsMultiLeg() {
		contract, resolvedLegs, err = c.buildComboContract(order)
	} else {
		contract, err = c.qualifyContract(order.Instrument)
	}
	if err != nil {
		return nil, err
	}

	ibOrder, err := buildOrder(order)
	if err != nil {
		return nil, err
	}

	c.log.Info("placing order",
		"symbol", order.DisplaySymbol(),
		"direction", order.Direction,
		"quantity", order.Quantity,
		"type", order.OrderType,
		"structure", order.Structure,
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
	legFills := make([]model.TradeLeg, 0, len(resolvedLegs))
	legTotals := make(map[string]*model.TradeLeg)

	for _, fill := range fills {
		if fill == nil || fill.Execution == nil {
			continue
		}
		qty := fill.Execution.Shares.Float()
		if order.IsMultiLeg() {
			if fill.Contract != nil && fill.Contract.SecType == "BAG" {
				totalQty += qty
				totalCost += qty * fill.Execution.Price
			}
		} else {
			totalQty += qty
			totalCost += qty * fill.Execution.Price
		}
		totalCommission += fill.CommissionAndFeesReport.CommissionAndFees
		if !fill.Time.IsZero() {
			filledAt = fill.Time
		}

		if !order.IsMultiLeg() || fill.Contract == nil || fill.Execution == nil || fill.Contract.SecType == "BAG" {
			continue
		}
		key := contractKey(fill.Contract)
		leg, ok := legTotals[key]
		if !ok {
			leg = &model.TradeLeg{
				Instrument: instrumentFromContract(fill.Contract),
				Direction:  directionFromExecution(fill.Execution.Side),
			}
			legTotals[key] = leg
		}
		leg.Quantity += qty
		leg.EntryPrice = weightedAveragePrice(leg.EntryPrice, fill.Execution.Price, leg.Quantity-qty, qty)
	}

	if order.IsMultiLeg() {
		if totalQty <= 0 {
			totalQty = order.Quantity
		}
		if totalCost <= 0 && trade.OrderStatus.AvgFillPrice > 0 {
			totalCost = trade.OrderStatus.AvgFillPrice * totalQty
		}
		for _, leg := range resolvedLegs {
			key := leg.Instrument.Key()
			if resolved, ok := legTotals[key]; ok {
				leg.Quantity = resolved.Quantity
				leg.EntryPrice = resolved.EntryPrice
				if leg.Direction == "" {
					leg.Direction = resolved.Direction
				}
			} else {
				leg.Quantity = leg.EffectiveQuantity(order.Quantity)
			}
			legFills = append(legFills, leg)
		}
		sort.Slice(legFills, func(i, j int) bool {
			return legFills[i].Instrument.Key() < legFills[j].Instrument.Key()
		})
	} else if totalQty <= 0 {
		return nil, fmt.Errorf("order filled with non-positive quantity")
	}

	avgPrice := order.LimitPrice
	if totalQty > 0 && totalCost > 0 {
		avgPrice = totalCost / totalQty
	}
	instrument := order.Instrument
	if order.IsMultiLeg() {
		instrument = order.PrimaryInstrument()
		if len(legFills) > 0 && instrument.Multiplier == "" {
			instrument.Multiplier = legFills[0].Instrument.Multiplier
		}
	} else {
		instrument.ConID = contract.ConID
		if contract.Multiplier != "" {
			instrument.Multiplier = contract.Multiplier
		}
	}
	if contract.Multiplier != "" {
		instrument.Multiplier = contract.Multiplier
	}

	return &model.Fill{
		OrderID:     order.ID,
		IBKROrderID: trade.Order.OrderID,
		Structure:   order.Structure,
		Instrument:  instrument,
		Legs:        legFills,
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

func (c *Client) buildComboContract(order model.Order) (*ibsync.Contract, []model.TradeLeg, error) {
	if len(order.Legs) < 2 {
		return nil, nil, fmt.Errorf("multi-leg order requires at least two legs")
	}

	combo := ibsync.NewBag()
	primary := order.PrimaryInstrument()
	combo.Symbol = primary.Symbol
	combo.Exchange = primary.Exchange
	combo.Currency = primary.Currency
	if combo.Exchange == "" {
		combo.Exchange = "SMART"
	}
	if combo.Currency == "" {
		combo.Currency = "USD"
	}
	combo.ComboLegsDescrip = order.DisplaySymbol()

	resolved := make([]model.TradeLeg, 0, len(order.Legs))
	combo.ComboLegs = make([]ibapi.ComboLeg, 0, len(order.Legs))
	for _, leg := range order.Legs {
		qualified, err := c.qualifyContract(leg.Instrument)
		if err != nil {
			return nil, nil, fmt.Errorf("qualify combo leg %s: %w", leg.Instrument.Label(), err)
		}

		resolvedLeg := leg
		resolvedLeg.Instrument = instrumentFromContract(qualified)
		if resolvedLeg.Direction == "" {
			resolvedLeg.Direction = model.Long
		}
		resolved = append(resolved, resolvedLeg)

		comboLeg := ibapi.NewComboLeg()
		comboLeg.ConID = qualified.ConID
		comboLeg.Ratio = int64(math.Max(1, math.Round(leg.EffectiveRatio())))
		comboLeg.Action = actionFromDirection(resolvedLeg.Direction)
		comboLeg.Exchange = qualified.Exchange
		if comboLeg.Exchange == "" {
			comboLeg.Exchange = combo.Exchange
		}
		comboLeg.OpenClose = int64(ibapi.OPEN_POS)
		combo.ComboLegs = append(combo.ComboLegs, comboLeg)
	}

	return combo, resolved, nil
}

func buildOrder(order model.Order) (*ibapi.Order, error) {
	action := actionFromDirection(order.Direction)

	secType := order.Instrument.SecType
	if order.IsMultiLeg() {
		secType = "BAG"
	}
	qty := normalizeQuantity(order.Quantity, secType)
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

func actionFromDirection(direction model.TradeDirection) string {
	if direction == model.Short {
		return "SELL"
	}
	return "BUY"
}

func directionFromExecution(side string) model.TradeDirection {
	if side == "SLD" || side == "SELL" || side == "SSHORT" {
		return model.Short
	}
	return model.Long
}

func instrumentFromContract(contract *ibsync.Contract) model.Instrument {
	if contract == nil {
		return model.Instrument{}
	}
	return model.Instrument{
		ConID:      contract.ConID,
		Symbol:     contract.Symbol,
		SecType:    contract.SecType,
		Exchange:   contract.Exchange,
		Currency:   contract.Currency,
		Expiry:     contract.LastTradeDateOrContractMonth,
		Strike:     contract.Strike,
		Right:      contract.Right,
		Multiplier: contract.Multiplier,
	}
}

func contractKey(contract *ibsync.Contract) string {
	return instrumentFromContract(contract).Key()
}

func weightedAveragePrice(currentAvg, newPrice, currentQty, newQty float64) float64 {
	totalQty := currentQty + newQty
	if totalQty <= 0 {
		return 0
	}
	return ((currentAvg * currentQty) + (newPrice * newQty)) / totalQty
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
