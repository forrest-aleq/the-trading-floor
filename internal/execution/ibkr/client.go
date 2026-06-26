package ibkr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
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
	pnlMu         sync.RWMutex
	pnlByKey      map[string]AccountPnL
	pnlSubs       map[string]struct{}
}

type connectionAPI interface {
	Connect(context.Context) error
	Disconnect()
	IB() *ibsync.IB
	IsConnected() bool
	IsPaper() bool
	RunReconnectLoop(context.Context)
	Status() ConnectionStatus
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
	NetLiquidation      float64
	BuyingPower         float64
	Cash                float64
	EquityWithLoanValue float64
	GrossPositionValue  float64
	RegTEquity          float64
	RegTMargin          float64
	SMA                 float64
	InitMarginReq       float64
	MaintMarginReq      float64
	AvailableFunds      float64
	ExcessLiquidity     float64
	UnrealizedPnL       float64
	RealizedPnL         float64
	DailyPnL            float64
	DailyPnLReady       bool
	PnLSource           string
	PnLAccount          string
	PnLError            string
}

type AccountPnL struct {
	Account       string
	ModelCode     string
	DailyPnL      float64
	UnrealizedPnL float64
	RealizedPnL   float64
	UpdatedAt     time.Time
}

type HistoricalBar struct {
	Time  time.Time
	Open  float64
	High  float64
	Low   float64
	Close float64
}

const accountSummaryTags = "NetLiquidation,TotalCashValue,BuyingPower,EquityWithLoanValue,GrossPositionValue,RegTEquity,RegTMargin,SMA,InitMarginReq,MaintMarginReq,AvailableFunds,ExcessLiquidity,UnrealizedPnL,RealizedPnL"

var ErrOrderNotFound = errors.New("order not found")

const defaultOrderAckTimeout = 12 * time.Second

func init() {
	configureIBKRMessageEncoding()
}

func configureIBKRMessageEncoding() {
	if readBoolEnv("IBKR_USE_PROTOBUF_ORDER_MESSAGES", false) {
		return
	}
	// TWS/Gateway server versions >= 203 advertise protobuf order messages, but
	// the scmhub v0.10.x protobuf place-order path can leave orders ApiPending
	// without a broker acknowledgement. Keep account/market-data protobuf paths,
	// while forcing the order lifecycle through the older field encoder.
	delete(ibapi.PROTOBUF_MSG_IDS, ibapi.PLACE_ORDER)
	delete(ibapi.PROTOBUF_MSG_IDS, ibapi.CANCEL_ORDER)
	delete(ibapi.PROTOBUF_MSG_IDS, ibapi.REQ_GLOBAL_CANCEL)
}

type PendingOrderError struct {
	OrderID int64
	Status  string
	Reason  string
}

type UnacknowledgedOrderError struct {
	OrderID        int64
	Status         string
	LastLogCode    int64
	LastLogMessage string
	Cause          error
}

func (e *UnacknowledgedOrderError) Error() string {
	if e == nil {
		return ""
	}
	reason := "order not acknowledged by broker"
	if e.Status != "" {
		reason += "; last status=" + e.Status
	}
	if e.OrderID > 0 {
		reason += fmt.Sprintf("; broker_order_id=%d", e.OrderID)
	}
	if e.LastLogCode != 0 || e.LastLogMessage != "" {
		reason += fmt.Sprintf("; last_broker_log_code=%d", e.LastLogCode)
		if e.LastLogMessage != "" {
			reason += "; last_broker_log=" + e.LastLogMessage
		}
	}
	if strings.EqualFold(e.Status, string(ibsync.ApiPending)) {
		reason += "; hint=check TWS API order precautions and Bypass Order Precautions for API Orders"
	}
	if e.Cause != nil {
		reason += ": " + e.Cause.Error()
	}
	return reason
}

func (e *UnacknowledgedOrderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type OrderLookup struct {
	OrderID           int64
	Status            string
	FilledQuantity    float64
	RemainingQuantity float64
	AvgFillPrice      float64
	LastFillPrice     float64
	UpdatedAt         time.Time
	Active            bool
	Done              bool
	Fill              *model.Fill
}

type OpenOrderSnapshot struct {
	OrderID           int64
	PermID            int64
	ClientID          int64
	Symbol            string
	LocalSymbol       string
	SecType           string
	Exchange          string
	PrimaryExchange   string
	Currency          string
	Action            string
	OrderType         string
	TotalQuantity     float64
	LmtPrice          float64
	AuxPrice          float64
	TIF               string
	OutsideRTH        bool
	Transmit          bool
	AlgoStrategy      string
	Status            string
	FilledQuantity    float64
	RemainingQuantity float64
	AvgFillPrice      float64
	LastFillPrice     float64
	WhyHeld           string
	MktCapPrice       float64
}

func (e *PendingOrderError) Error() string {
	if e == nil {
		return "pending order"
	}
	if e.Reason != "" {
		return e.Reason
	}
	if e.Status != "" {
		return fmt.Sprintf("order %d pending with status %s", e.OrderID, e.Status)
	}
	return fmt.Sprintf("order %d pending", e.OrderID)
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

func (c *Client) ConnectionStatus() ConnectionStatus {
	return c.conn.Status()
}

func (c *Client) Close() {
	c.conn.Disconnect()
}

func BuildContract(inst model.Instrument) *ibsync.Contract {
	originalSymbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	inst = normalizeIBKRInstrument(inst)
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
	if _, suffix, ok := splitListingSuffix(originalSymbol); ok {
		if hint, ok := listingSuffixHints[suffix]; ok {
			contract.PrimaryExchange = hint.primaryExchange
		}
	}
	if inst.Expiry != "" {
		contract.LastTradeDateOrContractMonth = inst.Expiry
	}
	if inst.Multiplier != "" {
		contract.Multiplier = inst.Multiplier
	}
	return contract
}

type listingHint struct {
	currency        string
	exchange        string
	primaryExchange string
}

var listingSuffixHints = map[string]listingHint{
	"AS": {currency: "EUR", exchange: "SMART", primaryExchange: "AEB"},
	"AX": {currency: "AUD", exchange: "SMART", primaryExchange: "ASX"},
	"DE": {currency: "EUR", exchange: "SMART", primaryExchange: "IBIS"},
	"HK": {currency: "HKD", exchange: "SMART", primaryExchange: "SEHK"},
	"KS": {currency: "KRW", exchange: "SMART", primaryExchange: "KSE"},
	"KQ": {currency: "KRW", exchange: "SMART", primaryExchange: "KOSDAQ"},
	"L":  {currency: "GBP", exchange: "SMART", primaryExchange: "LSE"},
	"LN": {currency: "GBP", exchange: "SMART", primaryExchange: "LSE"},
	"MI": {currency: "EUR", exchange: "SMART", primaryExchange: "BVME"},
	"PA": {currency: "EUR", exchange: "SMART", primaryExchange: "SBF"},
	"SW": {currency: "CHF", exchange: "SMART", primaryExchange: "EBS"},
	"T":  {currency: "JPY", exchange: "SMART", primaryExchange: "TSEJ"},
	"TO": {currency: "CAD", exchange: "SMART", primaryExchange: "TSE"},
	"V":  {currency: "CAD", exchange: "SMART", primaryExchange: "VENTURE"},
}

func normalizeIBKRInstrument(inst model.Instrument) model.Instrument {
	secType := strings.ToUpper(strings.TrimSpace(inst.SecType))
	if secType != "" && secType != "STK" && secType != "ETF" {
		return inst
	}
	if secType == "ETF" {
		inst.SecType = "STK"
	}
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	base, suffix, ok := splitListingSuffix(symbol)
	if !ok {
		inst.Symbol = symbol
		return inst
	}
	hint, ok := listingSuffixHints[suffix]
	if !ok {
		inst.Symbol = symbol
		return inst
	}
	inst.Symbol = base
	if strings.TrimSpace(inst.Currency) == "" || strings.EqualFold(inst.Currency, "USD") {
		inst.Currency = hint.currency
	}
	if strings.TrimSpace(inst.Exchange) == "" || strings.EqualFold(inst.Exchange, "SMART") {
		inst.Exchange = hint.exchange
	}
	return inst
}

func splitListingSuffix(symbol string) (base string, suffix string, ok bool) {
	idx := strings.LastIndex(symbol, ".")
	if idx <= 0 || idx == len(symbol)-1 {
		return "", "", false
	}
	base = strings.TrimSpace(symbol[:idx])
	suffix = strings.TrimSpace(symbol[idx+1:])
	if base == "" || suffix == "" {
		return "", "", false
	}
	return base, suffix, true
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
		contract, resolvedLegs, err = c.buildComboContract(ctx, order)
	} else {
		contract, err = c.qualifyContract(ctx, order.Instrument)
	}
	if err != nil {
		return nil, err
	}

	if err := validateBrokerLotSize(order, contract); err != nil {
		return nil, err
	}

	ibOrder, err := buildOrder(order)
	if err != nil {
		return nil, err
	}
	if account := resolveOrderAccount(ib.ManagedAccounts()); account != "" {
		ibOrder.Account = account
	}
	if c.IsPaper() || readBoolEnv("IBKR_ALLOW_OUTSIDE_RTH", false) {
		ibOrder.OutsideRTH = true
	}
	status := c.conn.Status()
	ibOrder.ClientID = int64(status.ClientID)
	ibOrder.OrderRef = orderReference(order.ID)

	c.log.Info("placing order",
		"symbol", order.DisplaySymbol(),
		"direction", order.Direction,
		"quantity", order.Quantity,
		"type", order.OrderType,
		"limit_price", order.LimitPrice,
		"stop_price", order.StopPrice,
		"tif", ibOrder.TIF,
		"outside_rth", ibOrder.OutsideRTH,
		"transmit", ibOrder.Transmit,
		"account_set", ibOrder.Account != "",
		"ib_order_type", ibOrder.OrderType,
		"algo_strategy", ibOrder.AlgoStrategy,
		"client_id", ibOrder.ClientID,
		"order_ref", ibOrder.OrderRef,
		"protobuf_order_messages", ibkrOrderLifecycleUsesProtobuf(),
		"con_id", contract.ConID,
		"exchange", contract.Exchange,
		"primary_exchange", contract.PrimaryExchange,
		"currency", contract.Currency,
		"structure", order.Structure,
		"paper", c.IsPaper(),
	)

	trade := ib.PlaceOrder(contract, ibOrder)
	if trade == nil {
		return nil, fmt.Errorf("place order returned nil trade")
	}
	c.log.Info("broker order id assigned",
		"symbol", order.DisplaySymbol(),
		"broker_order_id", trade.Order.OrderID,
		"client_id", ibOrder.ClientID,
		"order_ref", ibOrder.OrderRef,
	)

	waitCtx, cancel := withDefaultTimeout(ctx, 60*time.Second)
	defer cancel()

	ackTimeout := readDurationEnv("IBKR_ORDER_ACK_TIMEOUT", defaultOrderAckTimeout)
	ackTimer := time.NewTimer(ackTimeout)
	defer ackTimer.Stop()

	ackCh := trade.Ack()
	doneCh := trade.Done()
	pollInterval := readDurationEnv("IBKR_ORDER_ACK_POLL_INTERVAL", 250*time.Millisecond)
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	ackPoll := time.NewTicker(pollInterval)
	defer ackPoll.Stop()

	for {
		if err := terminalBrokerOrderError(trade); err != nil {
			return nil, err
		}
		if trade.OrderStatus.IsDone() {
			break
		}
		if pending := pendingOrderError(trade, nil); pending != nil {
			return nil, pending
		}

		select {
		case <-ackCh:
			ackCh = nil
		case <-ackPoll.C:
			if _, refreshErr := c.refreshOrderAcknowledgement(ctx, trade); refreshErr != nil {
				c.log.Warn("broker order acknowledgement refresh failed",
					"symbol", order.DisplaySymbol(),
					"broker_order_id", trade.Order.OrderID,
					"error", refreshErr,
				)
			}
		case <-ackTimer.C:
			return nil, unacknowledgedBrokerOrderError(trade, fmt.Errorf("broker acknowledgement timeout after %s", ackTimeout))
		case <-waitCtx.Done():
			if err := terminalBrokerOrderError(trade); err != nil {
				return nil, err
			}
			if pending := pendingOrderError(trade, waitCtx.Err()); pending != nil {
				if readBoolEnv("IBKR_CANCEL_PENDING_ON_TIMEOUT", false) {
					c.cancelPaperTrade(trade)
				}
				return nil, pending
			}
			return nil, unacknowledgedBrokerOrderError(trade, waitCtx.Err())
		case <-doneCh:
			doneCh = nil
		}
	}

	if err := terminalBrokerOrderError(trade); err != nil {
		return nil, err
	}
	if !trade.OrderStatus.IsDone() {
		if pending := pendingOrderError(trade, fmt.Errorf("order did not complete")); pending != nil {
			if readBoolEnv("IBKR_CANCEL_PENDING_ON_TIMEOUT", false) {
				c.cancelPaperTrade(trade)
			}
			return nil, pending
		}
		return nil, unacknowledgedBrokerOrderError(trade, fmt.Errorf("order did not complete"))
	}

	return materializeFill(order, trade, contract, resolvedLegs)
}

func (c *Client) refreshOrderAcknowledgement(ctx context.Context, trade *ibsync.Trade) (bool, error) {
	if trade == nil || trade.Order == nil {
		return false, nil
	}
	timeout := readDurationEnv("IBKR_OPEN_ORDER_REFRESH_TIMEOUT", 5*time.Second)
	refreshCtx, cancel := withDefaultTimeout(ctx, timeout)
	defer cancel()
	if _, err := c.OpenOrders(refreshCtx, false); err != nil {
		return false, err
	}
	status := strings.TrimSpace(string(trade.OrderStatus.Status))
	return status != "" && !strings.EqualFold(status, string(ibsync.PendingSubmit)), nil
}

func materializeFill(order model.Order, trade *ibsync.Trade, contract *ibsync.Contract, resolvedLegs []model.TradeLeg) (*model.Fill, error) {
	if trade == nil {
		return nil, fmt.Errorf("nil trade")
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

		if !order.IsMultiLeg() || fill.Contract == nil || fill.Contract.SecType == "BAG" {
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
	} else if contract != nil {
		instrument.ConID = contract.ConID
		if contract.Multiplier != "" {
			instrument.Multiplier = contract.Multiplier
		}
	}
	if contract != nil && contract.Multiplier != "" {
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

func pendingOrderError(trade *ibsync.Trade, cause error) *PendingOrderError {
	if trade == nil || trade.Order == nil {
		return nil
	}
	status := strings.TrimSpace(string(trade.OrderStatus.Status))
	if !isPendingTradeStatus(status) {
		return nil
	}
	reason := "order accepted by broker and fill pending"
	if cause != nil {
		reason += ": " + cause.Error()
	}
	return &PendingOrderError{
		OrderID: trade.Order.OrderID,
		Status:  status,
		Reason:  reason,
	}
}

func unacknowledgedBrokerOrderError(trade *ibsync.Trade, cause error) error {
	orderID := int64(0)
	status := ""
	lastLogCode := int64(0)
	lastLogMessage := ""
	if trade != nil {
		status = strings.TrimSpace(string(trade.OrderStatus.Status))
		if trade.Order != nil {
			orderID = trade.Order.OrderID
		}
		if lastLog, ok := latestBrokerLogEntry(trade.Logs()); ok {
			lastLogCode = lastLog.ErrorCode
			lastLogMessage = strings.TrimSpace(lastLog.Message)
		}
	}
	return &UnacknowledgedOrderError{
		OrderID:        orderID,
		Status:         status,
		LastLogCode:    lastLogCode,
		LastLogMessage: lastLogMessage,
		Cause:          cause,
	}
}

func terminalBrokerOrderError(trade *ibsync.Trade) error {
	if trade == nil || trade.Order == nil {
		return nil
	}
	if err := terminalBrokerLogError(trade.Order.OrderID, trade.Logs()); err != nil {
		return err
	}
	status := strings.TrimSpace(string(trade.OrderStatus.Status))
	switch strings.ToLower(status) {
	case "inactive", "cancelled", "apicancelled":
		return fmt.Errorf("broker rejected order %d: terminal status=%s", trade.Order.OrderID, status)
	default:
		return nil
	}
}

func terminalBrokerLogError(orderID int64, logs []ibsync.TradeLogEntry) error {
	for i := len(logs) - 1; i >= 0; i-- {
		entry := logs[i]
		if !isTerminalBrokerErrorCode(entry.ErrorCode) {
			continue
		}
		message := strings.TrimSpace(entry.Message)
		if message == "" {
			message = "broker rejected order"
		}
		return fmt.Errorf("broker rejected order %d: %s (code=%d)", orderID, message, entry.ErrorCode)
	}
	return nil
}

func latestBrokerLogEntry(logs []ibsync.TradeLogEntry) (ibsync.TradeLogEntry, bool) {
	for i := len(logs) - 1; i >= 0; i-- {
		entry := logs[i]
		if strings.TrimSpace(entry.Message) != "" || entry.ErrorCode != 0 {
			return entry, true
		}
	}
	return ibsync.TradeLogEntry{}, false
}

func isTerminalBrokerErrorCode(code int64) bool {
	switch code {
	case 110, 200, 201, 321, 388, 10052, 10243, 10268, 10318:
		return true
	default:
		return false
	}
}

func ibkrOrderLifecycleUsesProtobuf() bool {
	_, ok := ibapi.PROTOBUF_MSG_IDS[ibapi.PLACE_ORDER]
	return ok
}

func isPendingTradeStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "apisubmitted", "presubmitted", "submitted", "pendingcancel":
		return true
	default:
		return false
	}
}

func (c *Client) cancelPaperTrade(trade *ibsync.Trade) {
	if !c.IsPaper() || trade == nil || trade.Order == nil {
		return
	}
	ib := c.conn.IB()
	if ib == nil {
		return
	}
	ib.CancelOrder(trade.Order, ibapi.NewOrderCancel())
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

	return fmt.Errorf("%w: %d", ErrOrderNotFound, orderID)
}

func (c *Client) GetOrderStatus(ctx context.Context, order model.Order, orderID int64) (*OrderLookup, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	for _, trade := range ib.Trades() {
		if trade == nil || trade.Order == nil || trade.Order.OrderID != orderID {
			continue
		}
		lookup := &OrderLookup{
			OrderID:           orderID,
			Status:            strings.TrimSpace(string(trade.OrderStatus.Status)),
			FilledQuantity:    trade.OrderStatus.Filled.Float(),
			RemainingQuantity: trade.OrderStatus.Remaining.Float(),
			AvgFillPrice:      trade.OrderStatus.AvgFillPrice,
			LastFillPrice:     trade.OrderStatus.LastFillPrice,
			UpdatedAt:         time.Now().UTC(),
			Active:            isPendingTradeStatus(string(trade.OrderStatus.Status)),
			Done:              trade.IsDone() || trade.OrderStatus.IsDone(),
		}
		if lookup.Done {
			fill, err := materializeFill(order, trade, trade.Contract, append([]model.TradeLeg(nil), order.Legs...))
			if err == nil {
				lookup.Fill = fill
			}
		}
		return lookup, nil
	}

	return nil, fmt.Errorf("%w: %d", ErrOrderNotFound, orderID)
}

func (c *Client) OpenOrders(ctx context.Context, allClients bool) ([]OpenOrderSnapshot, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}
	if err := runBlockingIBCall(ctx, func() error {
		if allClients {
			return ib.ReqAllOpenOrders()
		}
		return ib.ReqOpenOrders()
	}); err != nil {
		return nil, fmt.Errorf("request open orders: %w", err)
	}

	trades := ib.OpenTrades()
	snapshots := make([]OpenOrderSnapshot, 0, len(trades))
	for _, trade := range trades {
		if trade == nil || trade.Order == nil {
			continue
		}
		order := trade.Order
		status := trade.OrderStatus
		snapshot := OpenOrderSnapshot{
			OrderID:           order.OrderID,
			PermID:            status.PermID,
			ClientID:          status.ClientID,
			Action:            order.Action,
			OrderType:         order.OrderType,
			TotalQuantity:     order.TotalQuantity.Float(),
			LmtPrice:          order.LmtPrice,
			AuxPrice:          order.AuxPrice,
			TIF:               order.TIF,
			OutsideRTH:        order.OutsideRTH,
			Transmit:          order.Transmit,
			AlgoStrategy:      order.AlgoStrategy,
			Status:            strings.TrimSpace(string(status.Status)),
			FilledQuantity:    status.Filled.Float(),
			RemainingQuantity: status.Remaining.Float(),
			AvgFillPrice:      status.AvgFillPrice,
			LastFillPrice:     status.LastFillPrice,
			WhyHeld:           status.WhyHeld,
			MktCapPrice:       status.MktCapPrice,
		}
		if trade.Contract != nil {
			snapshot.Symbol = trade.Contract.Symbol
			snapshot.LocalSymbol = trade.Contract.LocalSymbol
			snapshot.SecType = trade.Contract.SecType
			snapshot.Exchange = trade.Contract.Exchange
			snapshot.PrimaryExchange = trade.Contract.PrimaryExchange
			snapshot.Currency = trade.Contract.Currency
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].OrderID < snapshots[j].OrderID
	})
	return snapshots, nil
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
	if !readBoolEnv("IBKR_ACCOUNT_SUMMARY_SYNC", false) {
		return nil, nil
	}

	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	if summary, ok, err := accountSummaryFromValues(ib.AccountValues(), ib.ManagedAccounts()); err != nil {
		return nil, err
	} else if ok {
		c.attachAccountPnL(ctx, summary)
		return summary, nil
	}

	// Keep this request deliberately narrow. ibsync.AccountSummary() requests
	// the full Account Window tag set including ledgers, which can stall long
	// enough to make broker reconciliation look dead even when TWS is connected.
	timeout := readDurationEnv("IBKR_ACCOUNT_SUMMARY_TIMEOUT", 8*time.Second)
	ib.SetTimeout(timeout)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type accountSummaryResult struct {
		items ibsync.AccountSummary
		err   error
	}
	resultCh := make(chan accountSummaryResult, 1)
	go func() {
		items, err := ib.ReqAccountSummary("All", accountSummaryTags)
		resultCh <- accountSummaryResult{items: items, err: err}
	}()

	var items ibsync.AccountSummary
	select {
	case <-waitCtx.Done():
		return nil, fmt.Errorf("account summary timed out after %s: %w", timeout, waitCtx.Err())
	case result := <-resultCh:
		if result.err != nil {
			return nil, fmt.Errorf("account summary request failed: %w", result.err)
		}
		items = result.items
	}
	summary, ok, err := accountSummaryFromValues(ibsync.AccountValues(items), ib.ManagedAccounts())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("empty account summary")
	}
	c.attachAccountPnL(ctx, summary)
	return summary, nil
}

func accountSummaryFromValues(items ibsync.AccountValues, managedAccounts []string) (*AccountSummary, bool, error) {
	if len(items) == 0 {
		return nil, false, nil
	}
	accountCandidates := make([]string, 0, 2)
	for _, item := range items {
		if account := strings.TrimSpace(item.Account); account != "" && !strings.EqualFold(account, "all") {
			accountCandidates = append(accountCandidates, account)
		}
	}
	selectedAccount := resolveBrokerDataAccount(managedAccounts, accountCandidates)
	if selectedAccount == "" && multipleUniqueAccounts(accountCandidates) {
		return nil, false, fmt.Errorf("account summary ambiguous across multiple accounts; set IBKR_ACCOUNT or IBKR_PNL_ACCOUNT")
	}

	summary := &AccountSummary{PnLAccount: selectedAccount}
	foundAccountMetric := false
	for _, item := range items {
		if selectedAccount != "" {
			account := strings.TrimSpace(item.Account)
			if account != "" && !strings.EqualFold(account, selectedAccount) {
				continue
			}
		}
		switch item.Tag {
		case "NetLiquidation":
			summary.NetLiquidation = parseFloat(item.Value)
			foundAccountMetric = foundAccountMetric || summary.NetLiquidation > 0
		case "BuyingPower":
			summary.BuyingPower = parseFloat(item.Value)
			foundAccountMetric = foundAccountMetric || summary.BuyingPower != 0
		case "TotalCashValue":
			summary.Cash = parseFloat(item.Value)
			foundAccountMetric = foundAccountMetric || summary.Cash != 0
		case "EquityWithLoanValue":
			summary.EquityWithLoanValue = parseFloat(item.Value)
		case "GrossPositionValue":
			summary.GrossPositionValue = parseFloat(item.Value)
		case "RegTEquity":
			summary.RegTEquity = parseFloat(item.Value)
		case "RegTMargin":
			summary.RegTMargin = parseFloat(item.Value)
		case "SMA":
			summary.SMA = parseFloat(item.Value)
		case "InitMarginReq":
			summary.InitMarginReq = parseFloat(item.Value)
		case "MaintMarginReq":
			summary.MaintMarginReq = parseFloat(item.Value)
		case "AvailableFunds":
			summary.AvailableFunds = parseFloat(item.Value)
		case "ExcessLiquidity":
			summary.ExcessLiquidity = parseFloat(item.Value)
		case "UnrealizedPnL":
			summary.UnrealizedPnL = parseFloat(item.Value)
		case "RealizedPnL":
			summary.RealizedPnL = parseFloat(item.Value)
		}
	}
	if !foundAccountMetric {
		return nil, false, nil
	}
	return summary, true, nil
}

func (c *Client) attachAccountPnL(ctx context.Context, summary *AccountSummary) {
	if c == nil || summary == nil || !readBoolEnv("IBKR_ACCOUNT_PNL_SYNC", true) {
		return
	}
	if summary.PnLAccount == "" {
		summary.PnLError = "account pnl unavailable: set IBKR_ACCOUNT when multiple accounts are visible"
	} else if pnl, err := c.GetAccountPnL(ctx, summary.PnLAccount); err != nil {
		summary.PnLError = err.Error()
	} else {
		summary.DailyPnL = pnl.DailyPnL
		summary.UnrealizedPnL = pnl.UnrealizedPnL
		summary.RealizedPnL = pnl.RealizedPnL
		summary.DailyPnLReady = true
		summary.PnLSource = "ibkr_req_pnl"
	}
}

func (c *Client) GetAccountPnL(ctx context.Context, account string) (*AccountPnL, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return nil, fmt.Errorf("account pnl unavailable: account is required")
	}
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}
	modelCode := strings.TrimSpace(os.Getenv("IBKR_PNL_MODEL_CODE"))
	key := pnlSubscriptionKey(account, modelCode)
	if pnl, ok := c.cachedAccountPnL(key); ok {
		return &pnl, nil
	}

	c.ensureAccountPnLSubscription(ib, account, modelCode, key)

	timeout := readDurationEnv("IBKR_PNL_SYNC_TIMEOUT", 5*time.Second)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("account pnl timed out after %s for account %s: %w", timeout, account, waitCtx.Err())
		case <-ticker.C:
			if pnl, ok := c.cachedAccountPnL(key); ok {
				return &pnl, nil
			}
		}
	}
}

func (c *Client) cachedAccountPnL(key string) (AccountPnL, bool) {
	c.pnlMu.RLock()
	defer c.pnlMu.RUnlock()
	pnl, ok := c.pnlByKey[key]
	return pnl, ok
}

func (c *Client) ensureAccountPnLSubscription(ib *ibsync.IB, account string, modelCode string, key string) {
	c.pnlMu.Lock()
	if c.pnlByKey == nil {
		c.pnlByKey = make(map[string]AccountPnL)
	}
	if c.pnlSubs == nil {
		c.pnlSubs = make(map[string]struct{})
	}
	if _, exists := c.pnlSubs[key]; exists {
		c.pnlMu.Unlock()
		return
	}
	c.pnlSubs[key] = struct{}{}
	c.pnlMu.Unlock()

	ch := ib.PnlChan(account, modelCode)
	ib.ReqPnL(account, modelCode)
	go c.consumeAccountPnL(key, ch)
}

func (c *Client) consumeAccountPnL(key string, ch <-chan ibsync.Pnl) {
	defer func() {
		c.pnlMu.Lock()
		delete(c.pnlSubs, key)
		c.pnlMu.Unlock()
	}()
	for pnl := range ch {
		if !usableIBFloat(pnl.DailyPNL) {
			continue
		}
		c.pnlMu.Lock()
		c.pnlByKey[key] = AccountPnL{
			Account:       pnl.Account,
			ModelCode:     pnl.ModelCode,
			DailyPnL:      pnl.DailyPNL,
			UnrealizedPnL: finiteOrZero(pnl.UnrealizedPnl),
			RealizedPnL:   finiteOrZero(pnl.RealizedPNL),
			UpdatedAt:     time.Now(),
		}
		c.pnlMu.Unlock()
	}
}

func pnlSubscriptionKey(account string, modelCode string) string {
	return strings.TrimSpace(account) + "\x00" + strings.TrimSpace(modelCode)
}

func usableIBFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) < math.MaxFloat64/4
}

func finiteOrZero(value float64) float64 {
	if !usableIBFloat(value) {
		return 0
	}
	return value
}

func resolveBrokerDataAccount(managedAccounts []string, accountSummaryAccounts []string) string {
	if account := strings.TrimSpace(os.Getenv("IBKR_PNL_ACCOUNT")); account != "" {
		return account
	}
	uniqueSummaryAccount := singleUniqueAccount(accountSummaryAccounts)
	if uniqueSummaryAccount != "" {
		if envAccount := strings.TrimSpace(os.Getenv("IBKR_ACCOUNT")); envAccount != "" {
			return envAccount
		}
		return uniqueSummaryAccount
	}
	return resolveOrderAccount(managedAccounts)
}

func singleUniqueAccount(accounts []string) string {
	seen := ""
	for _, account := range accounts {
		account = strings.TrimSpace(account)
		if account == "" {
			continue
		}
		if seen != "" && seen != account {
			return ""
		}
		seen = account
	}
	return seen
}

func multipleUniqueAccounts(accounts []string) bool {
	first := ""
	for _, account := range accounts {
		account = strings.TrimSpace(account)
		if account == "" {
			continue
		}
		if first == "" {
			first = account
			continue
		}
		if account != first {
			return true
		}
	}
	return false
}

func (c *Client) ReqMarketData(ctx context.Context, inst model.Instrument) (*MarketData, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	contract, err := c.qualifyContract(ctx, inst)
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

func (c *Client) HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]HistoricalBar, error) {
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	contract, err := c.qualifyContract(ctx, inst)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(duration) == "" {
		duration = "3 D"
	}
	if strings.TrimSpace(barSize) == "" {
		barSize = "1 hour"
	}
	if strings.TrimSpace(whatToShow) == "" {
		whatToShow = historicalWhatToShow(inst)
	}

	waitCtx, cancel := withDefaultTimeout(ctx, 45*time.Second)
	defer cancel()

	barCh, stop := ib.ReqHistoricalData(contract, ibsync.FormatIBTimeUSEastern(end), duration, barSize, whatToShow, useRTH, 2)
	defer stop()

	bars := make([]HistoricalBar, 0, 128)
	for {
		select {
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		case bar, ok := <-barCh:
			if !ok {
				return bars, nil
			}
			observedAt, err := ibsync.ParseIBTime(bar.Date)
			if err != nil {
				return nil, fmt.Errorf("parse historical bar time %q: %w", bar.Date, err)
			}
			bars = append(bars, HistoricalBar{
				Time:  observedAt,
				Open:  bar.Open,
				High:  bar.High,
				Low:   bar.Low,
				Close: bar.Close,
			})
		}
	}
}

func (c *Client) qualifyContract(ctx context.Context, inst model.Instrument) (*ibsync.Contract, error) {
	if inst.IsKalshi() {
		return nil, fmt.Errorf("refusing prediction-market instrument %q for IBKR execution", inst.Symbol)
	}
	ib := c.conn.IB()
	if ib == nil {
		return nil, fmt.Errorf("not connected to IBKR")
	}

	contract := BuildContract(inst)
	waitCtx, cancel := withDefaultTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := runBlockingIBCall(waitCtx, func() error { return ib.QualifyContract(contract) }); err != nil {
		return nil, fmt.Errorf("qualify contract %s: %w", inst.Symbol, err)
	}
	normalizeStockOrderRoute(contract)

	return contract, nil
}

func runBlockingIBCall(ctx context.Context, fn func() error) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- fn()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func normalizeStockOrderRoute(contract *ibsync.Contract) {
	if contract == nil {
		return
	}
	secType := strings.ToUpper(strings.TrimSpace(contract.SecType))
	if secType == "ETF" {
		secType = "STK"
		contract.SecType = "STK"
	}
	if secType != "STK" {
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(contract.Currency))
	if currency != "" && currency != "USD" {
		return
	}
	exchange := strings.ToUpper(strings.TrimSpace(contract.Exchange))
	if !isDirectUSStockExchange(exchange) {
		return
	}
	if strings.TrimSpace(contract.PrimaryExchange) == "" || strings.EqualFold(contract.PrimaryExchange, contract.Exchange) {
		contract.PrimaryExchange = exchange
	}
	contract.Exchange = "SMART"
	if strings.TrimSpace(contract.Currency) == "" {
		contract.Currency = "USD"
	}
}

func isDirectUSStockExchange(exchange string) bool {
	switch strings.ToUpper(strings.TrimSpace(exchange)) {
	case "NYSE", "NASDAQ", "ARCA", "NYSEARCA", "AMEX", "NYSEAMEX", "NYSEMKT",
		"BATS", "BEX", "BYX", "CHX", "EDGEA", "EDGA", "EDGX", "IEX",
		"ISLAND", "LTSE", "MEMX", "PEARL", "PSX":
		return true
	default:
		return false
	}
}

func historicalWhatToShow(inst model.Instrument) string {
	switch strings.ToUpper(strings.TrimSpace(inst.SecType)) {
	case "CASH", "CFD":
		return "MIDPOINT"
	default:
		return "TRADES"
	}
}

func (c *Client) buildComboContract(ctx context.Context, order model.Order) (*ibsync.Contract, []model.TradeLeg, error) {
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
		qualified, err := c.qualifyContract(ctx, leg.Instrument)
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
	ibOrder.Transmit = true

	switch order.OrderType {
	case model.OrderAdaptive:
		ibapi.FillAdaptiveParams(ibOrder, "Urgent")
	case model.OrderTWAP:
		ibOrder.AlgoStrategy = "Twap"
	}

	return ibOrder, nil
}

func resolveOrderAccount(managedAccounts []string) string {
	if account := strings.TrimSpace(os.Getenv("IBKR_ACCOUNT")); account != "" {
		return account
	}
	account := ""
	for _, candidate := range managedAccounts {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if account != "" {
			return ""
		}
		account = candidate
	}
	return account
}

func orderReference(orderID string) string {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return ""
	}
	const maxLen = 32
	var b strings.Builder
	b.Grow(maxLen)
	for i := 0; i < len(orderID) && b.Len() < maxLen; i++ {
		ch := orderID[i]
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteByte(ch)
		case ch >= 'A' && ch <= 'Z':
			b.WriteByte(ch)
		case ch >= '0' && ch <= '9':
			b.WriteByte(ch)
		case ch == '-' || ch == '_':
			b.WriteByte(ch)
		}
	}
	return b.String()
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
	switch strings.ToUpper(strings.TrimSpace(secType)) {
	case "", "STK", "ETF":
		if quantity < 1 {
			return 0
		}
		return math.Floor(quantity)
	case "OPT", "FUT", "FOP":
		return math.Round(quantity)
	default:
		return quantity
	}
}

func validateBrokerLotSize(order model.Order, contract *ibsync.Contract) error {
	if order.IsMultiLeg() || contract == nil {
		return nil
	}
	secType := strings.ToUpper(strings.TrimSpace(contract.SecType))
	if secType == "" {
		secType = strings.ToUpper(strings.TrimSpace(order.Instrument.SecType))
	}
	if secType != "" && secType != "STK" && secType != "ETF" {
		return nil
	}

	lotSize := minimumStockLotSize(order.Instrument, contract)
	if lotSize <= 1 {
		return nil
	}
	quantity := normalizeQuantity(order.Quantity, secType)
	lotSizeFloat := float64(lotSize)
	if quantity < lotSizeFloat {
		return fmt.Errorf("broker minimum lot size for %s is %d shares; requested %.4f", order.DisplaySymbol(), lotSize, order.Quantity)
	}
	if math.Mod(quantity, lotSizeFloat) != 0 {
		return fmt.Errorf("broker lot size for %s is %d shares; requested %.4f", order.DisplaySymbol(), lotSize, order.Quantity)
	}
	return nil
}

func minimumStockLotSize(inst model.Instrument, contract *ibsync.Contract) int64 {
	if contract == nil {
		return 1
	}
	primary := strings.ToUpper(strings.TrimSpace(contract.PrimaryExchange))
	exchange := strings.ToUpper(strings.TrimSpace(contract.Exchange))
	currency := strings.ToUpper(strings.TrimSpace(contract.Currency))
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	switch {
	case primary == "TSEJ" || exchange == "TSEJ" || (currency == "JPY" && strings.HasSuffix(symbol, ".T")):
		return 100
	default:
		return 1
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
