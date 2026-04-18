package book

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

type PositionSource interface {
	GetPositions(context.Context) ([]ibkr.IBKRPosition, error)
}

type AccountSource interface {
	GetAccountSummary(context.Context) (*ibkr.AccountSummary, error)
}

// Book is the source of truth for portfolio state.
type Book struct {
	mu  sync.RWMutex
	log *slog.Logger

	positions         map[string]*model.Position // position_id -> position
	positionSource    PositionSource
	accountSource     AccountSource
	nav               float64
	cash              float64
	grossExposure     float64
	netExposure       float64
	dailyPnL          float64
	weeklyPnL         float64
	monthlyPnL        float64
	totalPnL          float64
	maxDrawdown       float64
	peakNAV           float64
	deskPnL           map[string]float64
	deskPositions     map[string]int
	deskCapital       map[string]float64
	totalTrades       int64
	initialCapital    float64
	reconcileInterval time.Duration
	brokerSync        brokerAccountState
}

type brokerAccountState struct {
	connected           bool
	nav                 float64
	cash                float64
	dailyPnL            float64
	unrealizedPnL       float64
	realizedPnL         float64
	openPositions       int
	grossExposure       float64
	netExposure         float64
	lastSynced          time.Time
	lastAccountSynced   time.Time
	lastPositionsSynced time.Time
	lastFailure         time.Time
	lastError           string
	consecutiveFailures int
	lastErrorLoggedAt   time.Time
}

type BrokerSyncStatus struct {
	Connected           bool
	NAV                 float64
	Cash                float64
	DailyPnL            float64
	UnrealizedPnL       float64
	RealizedPnL         float64
	OpenPositions       int
	GrossExposure       float64
	NetExposure         float64
	LastSynced          time.Time
	LastAccountSynced   time.Time
	LastPositionsSynced time.Time
	LastFailure         time.Time
	LastError           string
	ConsecutiveFailures int
}

type Discrepancy struct {
	Symbol      string
	BookQty     float64
	IBKRQty     float64
	BookAvgCost float64
	IBKRAvgCost float64
}

const minShadowEntryPrice = 0.01
const brokerRecoveryDeskID = "broker-recovery"
const brokerSyncErrorLogInterval = 30 * time.Second

func NewBook(positionSource PositionSource, initialCapital float64) *Book {
	var accountSource AccountSource
	if source, ok := positionSource.(AccountSource); ok {
		accountSource = source
	}
	return &Book{
		log:               slog.Default().With("component", "book"),
		positions:         make(map[string]*model.Position),
		positionSource:    positionSource,
		accountSource:     accountSource,
		nav:               initialCapital,
		cash:              initialCapital,
		peakNAV:           initialCapital,
		initialCapital:    initialCapital,
		deskPnL:           make(map[string]float64),
		deskPositions:     make(map[string]int),
		deskCapital:       make(map[string]float64),
		reconcileInterval: 60 * time.Second,
	}
}

func (b *Book) SetDeskCapital(deskID string, capital float64) {
	b.mu.Lock()
	b.deskCapital[deskID] = capital
	b.mu.Unlock()
}

func (b *Book) OpenPosition(fill *model.Fill, thesis *model.Thesis) *model.Position {
	b.mu.Lock()
	defer b.mu.Unlock()

	if fill != nil {
		if existing, ok := b.positions[fill.OrderID]; ok && existing != nil && existing.Status == "open" && !existing.Shadow {
			return existing
		}
	}

	pos := b.newPosition(fill, thesis)
	b.positions[pos.ID] = pos
	b.deskPositions[pos.DeskID]++
	b.totalTrades++

	notional := positionCashNotional(pos, fill.AvgPrice)
	if fill.Direction == model.Long {
		b.cash -= notional
	} else {
		b.cash += notional
	}
	b.recalculateLocked()

	b.log.Info("position opened",
		"id", pos.ID,
		"desk", pos.DeskID,
		"symbol", pos.DisplaySymbol(),
		"direction", pos.Direction,
		"qty", pos.Quantity,
		"price", pos.EntryPrice,
		"notional", notional,
	)

	return pos
}

func (b *Book) OpenShadowPosition(thesis *model.Thesis) *model.Position {
	b.mu.Lock()
	defer b.mu.Unlock()

	entryPrice := shadowEntryPrice(thesis)
	fill := &model.Fill{
		OrderID:    thesis.ID,
		Instrument: thesis.Instrument,
		Direction:  thesis.Direction,
		Quantity:   thesis.PositionSize,
		AvgPrice:   entryPrice,
		FilledAt:   time.Now(),
	}
	pos := b.newPosition(fill, thesis)
	pos.Shadow = true
	b.positions[pos.ID] = pos
	b.recalculateLocked()

	b.log.Info("shadow position opened",
		"id", pos.ID,
		"desk", pos.DeskID,
		"symbol", pos.DisplaySymbol(),
		"direction", pos.Direction,
		"qty", pos.Quantity,
		"price", pos.EntryPrice,
	)

	return pos
}

func (b *Book) newPosition(fill *model.Fill, thesis *model.Thesis) *model.Position {
	entryPrice := resolvedPositionPrice(fill, thesis)
	legs := make([]model.TradeLeg, 0)
	switch {
	case fill != nil && len(fill.Legs) > 0:
		legs = append(legs, fill.Legs...)
	case thesis != nil && len(thesis.Legs) > 0:
		legs = append(legs, thesis.Legs...)
	}
	for i := range legs {
		if legs[i].EntryPrice <= 0 {
			legs[i].EntryPrice = entryPrice
		}
		if legs[i].Quantity <= 0 {
			legs[i].Quantity = legs[i].EffectiveQuantity(fill.Quantity)
		}
	}

	pos := &model.Position{
		ID:             fill.OrderID,
		ThesisID:       thesis.ID,
		DeskID:         thesis.DeskID,
		Structure:      thesis.Structure,
		Instrument:     fill.PrimaryInstrument(),
		Legs:           legs,
		Direction:      fill.Direction,
		Quantity:       fill.Quantity,
		EntryPrice:     entryPrice,
		CurrentPrice:   entryPrice,
		IBKROrderID:    fill.IBKROrderID,
		IBKRContractID: fill.PrimaryInstrument().ConID,
		Status:         "open",
		OpenedAt:       fill.FilledAt,
	}
	if pos.OpenedAt.IsZero() {
		pos.OpenedAt = time.Now()
	}
	return pos
}

func shadowEntryPrice(thesis *model.Thesis) float64 {
	return resolvedPositionPrice(nil, thesis)
}

func resolvedPositionPrice(fill *model.Fill, thesis *model.Thesis) float64 {
	candidates := []float64{}
	if fill != nil {
		candidates = append(candidates, fill.AvgPrice)
	}
	if thesis != nil {
		candidates = append(candidates, thesis.EntryPrice)
		if thesis.MarketContext != nil {
			candidates = append(candidates, thesis.MarketContext.CurrentPrice)
		}
		candidates = append(candidates, thesis.TargetPrice, thesis.StopLoss)
	}
	for _, price := range candidates {
		if price > 0 {
			return price
		}
	}
	return minShadowEntryPrice
}

func (b *Book) ClosePosition(positionID string, exitPrice float64, exitReason string) (*model.ThesisOutcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	pos, ok := b.positions[positionID]
	if !ok {
		return nil, nil
	}
	if pos.Status != "open" {
		return nil, nil
	}

	notional := positionCashNotional(pos, exitPrice)
	if !pos.Shadow {
		if pos.Direction == model.Long {
			b.cash += notional
		} else {
			b.cash -= notional
		}
	}

	var pnl float64
	multiplier := pos.PrimaryInstrument().MultiplierValue()
	if pos.Direction == model.Long {
		pnl = (exitPrice - pos.EntryPrice) * pos.Quantity * multiplier
	} else {
		pnl = (pos.EntryPrice - exitPrice) * pos.Quantity * multiplier
	}

	pos.RealizedPnL = pnl
	pos.CurrentPrice = exitPrice
	pos.Status = "closed"
	now := time.Now()
	pos.ClosedAt = &now

	if !pos.Shadow {
		b.deskPnL[pos.DeskID] += pnl
		if b.deskPositions[pos.DeskID] > 0 {
			b.deskPositions[pos.DeskID]--
		}
		b.dailyPnL += pnl
	}
	b.recalculateLocked()

	holdingHours := now.Sub(pos.OpenedAt).Hours()
	entryNotional := positionCashNotional(pos, pos.EntryPrice)
	returnPct := 0.0
	if entryNotional > 0 {
		returnPct = (pnl / entryNotional) * 100
	}

	b.log.Info("position closed",
		"id", pos.ID,
		"desk", pos.DeskID,
		"symbol", pos.DisplaySymbol(),
		"pnl", pnl,
		"reason", exitReason,
		"held_hours", holdingHours,
	)

	return &model.ThesisOutcome{
		Profitable:   pnl > 0,
		RealizedPnL:  pnl,
		ReturnPct:    returnPct,
		HoldingHours: holdingHours,
		ExitReason:   exitReason,
	}, nil
}

func (b *Book) Mark(prices map[string]float64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, pos := range b.positions {
		if pos.Status != "open" || pos.Shadow {
			continue
		}

		if pos.IsMultiLeg() {
			updated := false
			current := 0.0
			for i := range pos.Legs {
				price, ok := lookupInstrumentPrice(prices, pos.Legs[i].Instrument)
				if !ok || price <= 0 {
					continue
				}
				pos.Legs[i].CurrentPrice = price
				current += pos.Legs[i].SignedPrice(price) * pos.Legs[i].EffectiveRatio()
				updated = true
			}
			if updated {
				pos.CurrentPrice = math.Abs(current)
			}
			continue
		}

		if price, ok := lookupInstrumentPrice(prices, pos.Instrument); ok && price > 0 {
			pos.CurrentPrice = price
		}
	}

	b.recalculateLocked()
}

func (b *Book) Snapshot() PortfolioSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()

	openCount := 0
	for _, pos := range b.positions {
		if pos.Status == "open" && !pos.Shadow {
			openCount++
		}
	}

	deskPnL := make(map[string]float64, len(b.deskPnL))
	for k, v := range b.deskPnL {
		deskPnL[k] = v
	}

	deskPos := make(map[string]int, len(b.deskPositions))
	for k, v := range b.deskPositions {
		deskPos[k] = v
	}

	deskCap := make(map[string]float64, len(b.deskCapital))
	for k, v := range b.deskCapital {
		deskCap[k] = v
	}

	nav := b.nav
	cash := b.cash
	dailyPnL := b.dailyPnL
	grossExposure := b.grossExposure
	netExposure := b.netExposure
	if b.brokerSync.connected && b.brokerSync.nav > 0 {
		nav = b.brokerSync.nav
		cash = b.brokerSync.cash
		dailyPnL = b.brokerSync.dailyPnL
		grossExposure = b.brokerSync.grossExposure
		netExposure = b.brokerSync.netExposure
		openCount = b.brokerSync.openPositions
	}

	return PortfolioSnapshot{
		NAV:           nav,
		Cash:          cash,
		GrossExposure: grossExposure,
		NetExposure:   netExposure,
		DailyPnL:      dailyPnL,
		WeeklyPnL:     b.weeklyPnL,
		MonthlyPnL:    b.monthlyPnL,
		TotalPnL:      b.totalPnL,
		MaxDrawdown:   b.maxDrawdown,
		OpenPositions: openCount,
		DeskPnL:       deskPnL,
		DeskPositions: deskPos,
		DeskCapital:   deskCap,
		TotalTrades:   b.totalTrades,
	}
}

func (b *Book) BrokerSyncStatus() BrokerSyncStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return BrokerSyncStatus{
		Connected:           b.brokerSync.connected,
		NAV:                 b.brokerSync.nav,
		Cash:                b.brokerSync.cash,
		DailyPnL:            b.brokerSync.dailyPnL,
		UnrealizedPnL:       b.brokerSync.unrealizedPnL,
		RealizedPnL:         b.brokerSync.realizedPnL,
		OpenPositions:       b.brokerSync.openPositions,
		GrossExposure:       b.brokerSync.grossExposure,
		NetExposure:         b.brokerSync.netExposure,
		LastSynced:          b.brokerSync.lastSynced,
		LastAccountSynced:   b.brokerSync.lastAccountSynced,
		LastPositionsSynced: b.brokerSync.lastPositionsSynced,
		LastFailure:         b.brokerSync.lastFailure,
		LastError:           b.brokerSync.lastError,
		ConsecutiveFailures: b.brokerSync.consecutiveFailures,
	}
}

type PortfolioSnapshot struct {
	NAV           float64
	Cash          float64
	GrossExposure float64
	NetExposure   float64
	DailyPnL      float64
	WeeklyPnL     float64
	MonthlyPnL    float64
	TotalPnL      float64
	MaxDrawdown   float64
	OpenPositions int
	DeskPnL       map[string]float64
	DeskPositions map[string]int
	DeskCapital   map[string]float64
	TotalTrades   int64
}

func (b *Book) StartReconcile(ctx context.Context) {
	b.reconcile(ctx)
	ticker := time.NewTicker(b.reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.reconcile(ctx)
		}
	}
}

func (b *Book) Reconcile(ibkrPositions []ibkr.IBKRPosition) []Discrepancy {
	b.mu.Lock()
	defer b.mu.Unlock()

	bookByKey := make(map[string]*model.Position)
	for _, pos := range b.positions {
		if pos.Status != "open" || pos.Shadow {
			continue
		}
		bookByKey[reconcileKey(pos.IBKRContractID, pos.Instrument.Symbol)] = pos
	}

	seen := make(map[string]bool)
	var discrepancies []Discrepancy

	for _, ip := range ibkrPositions {
		key := reconcileKey(ip.ConID, ip.Symbol)
		seen[key] = true

		pos, exists := bookByKey[key]
		if !exists {
			recovered := recoveredBrokerPosition(ip)
			b.positions[recovered.ID] = recovered
			if !recovered.Shadow {
				b.deskPositions[recovered.DeskID]++
			}
			discrepancies = append(discrepancies, Discrepancy{
				Symbol:      ip.Symbol,
				BookQty:     0,
				IBKRQty:     ip.Quantity,
				BookAvgCost: 0,
				IBKRAvgCost: ip.AvgCost,
			})
			continue
		}

		bookQty := signedPositionQuantity(pos)
		if bookQty != ip.Quantity || pos.EntryPrice != ip.AvgCost {
			applyBrokerRepair(pos, ip)
			discrepancies = append(discrepancies, Discrepancy{
				Symbol:      ip.Symbol,
				BookQty:     bookQty,
				IBKRQty:     ip.Quantity,
				BookAvgCost: pos.EntryPrice,
				IBKRAvgCost: ip.AvgCost,
			})
		}
	}

	for key, pos := range bookByKey {
		if seen[key] {
			continue
		}
		discrepancies = append(discrepancies, Discrepancy{
			Symbol:      pos.DisplaySymbol(),
			BookQty:     pos.Quantity,
			IBKRQty:     0,
			BookAvgCost: pos.EntryPrice,
			IBKRAvgCost: 0,
		})
	}

	b.recalculateLocked()
	return discrepancies
}

func (b *Book) GetOpenPositions() []*model.Position {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var open []*model.Position
	for _, pos := range b.positions {
		if pos.Status == "open" {
			open = append(open, pos)
		}
	}
	return open
}

func (b *Book) GetPosition(positionID string) (*model.Position, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	pos, ok := b.positions[positionID]
	return pos, ok
}

func (b *Book) ResetDaily() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.dailyPnL = 0
	for k := range b.deskPnL {
		b.deskPnL[k] = 0
	}
}

func (b *Book) recalculateLocked() {
	totalEquityAdjustment := 0.0
	totalUnrealized := 0.0
	b.grossExposure = 0
	b.netExposure = 0

	for _, pos := range b.positions {
		if pos.Status != "open" {
			continue
		}

		marketValue := positionNetMarketValue(pos)
		grossValue := positionGrossExposure(pos)
		if pos.Direction == model.Long {
			totalEquityAdjustment += marketValue
			b.netExposure += marketValue
			pos.UnrealizedPnL = (pos.CurrentPrice - pos.EntryPrice) * pos.Quantity * pos.PrimaryInstrument().MultiplierValue()
		} else {
			totalEquityAdjustment -= marketValue
			b.netExposure -= marketValue
			pos.UnrealizedPnL = (pos.EntryPrice - pos.CurrentPrice) * pos.Quantity * pos.PrimaryInstrument().MultiplierValue()
		}
		b.grossExposure += grossValue
		totalUnrealized += pos.UnrealizedPnL
	}

	b.nav = b.cash + totalEquityAdjustment
	b.totalPnL = b.nav - b.initialCapital
	b.weeklyPnL = b.totalPnL
	b.monthlyPnL = b.totalPnL

	if b.nav > b.peakNAV {
		b.peakNAV = b.nav
	}
	if b.peakNAV > 0 {
		drawdown := (b.peakNAV - b.nav) / b.peakNAV
		if drawdown > b.maxDrawdown {
			b.maxDrawdown = drawdown
		}
	}

	_ = totalUnrealized
}

func (b *Book) reconcile(ctx context.Context) {
	accountHealthy := true
	if b.accountSource != nil {
		summary, err := b.accountSource.GetAccountSummary(ctx)
		if err != nil {
			accountHealthy = false
			b.recordBrokerSyncFailure("account_summary", err)
		} else if summary != nil {
			b.applyAccountSummary(summary)
		}
	}

	positionsHealthy := true
	if b.positionSource == nil {
		if accountHealthy {
			b.markBrokerSyncHealthy()
		}
		return
	}

	ibkrPositions, err := b.positionSource.GetPositions(ctx)
	if err != nil {
		positionsHealthy = false
		b.recordBrokerSyncFailure("positions", err)
		return
	}

	b.applyBrokerPositions(ibkrPositions)
	if accountHealthy && positionsHealthy {
		b.markBrokerSyncHealthy()
	}
	discrepancies := b.Reconcile(ibkrPositions)
	for _, d := range discrepancies {
		b.log.Warn("reconciliation discrepancy",
			"symbol", d.Symbol,
			"book_qty", d.BookQty,
			"ibkr_qty", d.IBKRQty,
			"book_avg_cost", d.BookAvgCost,
			"ibkr_avg_cost", d.IBKRAvgCost,
		)
	}
}

func (b *Book) applyAccountSummary(summary *ibkr.AccountSummary) {
	if summary == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if summary.NetLiquidation > 0 {
		b.brokerSync.nav = summary.NetLiquidation
	}
	b.brokerSync.cash = summary.Cash
	b.brokerSync.unrealizedPnL = summary.UnrealizedPnL
	b.brokerSync.realizedPnL = summary.RealizedPnL
	b.brokerSync.dailyPnL = summary.UnrealizedPnL + summary.RealizedPnL
	b.brokerSync.lastAccountSynced = time.Now()
}

func (b *Book) applyBrokerPositions(positions []ibkr.IBKRPosition) {
	b.mu.Lock()
	defer b.mu.Unlock()

	openPositions := 0
	grossExposure := 0.0
	netExposure := 0.0
	for _, pos := range positions {
		if pos.Quantity == 0 {
			continue
		}
		openPositions++
		notional := math.Abs(pos.Quantity * pos.AvgCost)
		grossExposure += notional
		netExposure += pos.Quantity * pos.AvgCost
	}

	b.brokerSync.openPositions = openPositions
	b.brokerSync.grossExposure = grossExposure
	b.brokerSync.netExposure = netExposure
	b.brokerSync.lastPositionsSynced = time.Now()
}

func (b *Book) markBrokerSyncHealthy() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.brokerSync.connected = true
	b.brokerSync.lastSynced = time.Now()
	b.brokerSync.lastError = ""
	b.brokerSync.consecutiveFailures = 0
}

func (b *Book) recordBrokerSyncFailure(stage string, err error) {
	if err == nil {
		return
	}

	now := time.Now()
	message := stage + ": " + err.Error()

	b.mu.Lock()
	b.brokerSync.connected = false
	b.brokerSync.lastFailure = now
	b.brokerSync.lastError = message
	b.brokerSync.consecutiveFailures++
	failures := b.brokerSync.consecutiveFailures
	shouldLog := now.Sub(b.brokerSync.lastErrorLoggedAt) >= brokerSyncErrorLogInterval || b.brokerSync.lastErrorLoggedAt.IsZero()
	if shouldLog {
		b.brokerSync.lastErrorLoggedAt = now
	}
	b.mu.Unlock()

	if shouldLog {
		b.log.Error("broker sync failed",
			"stage", stage,
			"error", err,
			"consecutive_failures", failures,
		)
	}
}

func lookupInstrumentPrice(prices map[string]float64, inst model.Instrument) (float64, bool) {
	if price, ok := prices[inst.Key()]; ok && price > 0 {
		return price, true
	}
	if price, ok := prices[inst.Symbol]; ok && price > 0 {
		return price, true
	}
	return 0, false
}

func positionCashNotional(pos *model.Position, price float64) float64 {
	if pos == nil {
		return 0
	}
	if pos.IsMultiLeg() {
		return math.Abs(price * pos.Quantity * pos.PrimaryInstrument().MultiplierValue())
	}
	return pos.Instrument.Notional(price, pos.Quantity)
}

func positionNetMarketValue(pos *model.Position) float64 {
	if pos == nil {
		return 0
	}
	if pos.IsMultiLeg() {
		return math.Abs(pos.CurrentPrice * pos.Quantity * pos.PrimaryInstrument().MultiplierValue())
	}
	return pos.Instrument.Notional(pos.CurrentPrice, pos.Quantity)
}

func positionGrossExposure(pos *model.Position) float64 {
	if pos == nil {
		return 0
	}
	if !pos.IsMultiLeg() {
		return pos.Instrument.Notional(pos.CurrentPrice, pos.Quantity)
	}

	total := 0.0
	for _, leg := range pos.Legs {
		total += math.Abs(leg.Instrument.Notional(leg.CurrentOr(pos.CurrentPrice), leg.EffectiveQuantity(pos.Quantity)))
	}
	return total
}

func reconcileKey(conID int64, symbol string) string {
	if conID > 0 {
		return fmt.Sprintf("conid:%d", conID)
	}
	return "symbol:" + symbol
}

func signedPositionQuantity(pos *model.Position) float64 {
	if pos == nil {
		return 0
	}
	if pos.Direction == model.Short {
		return -pos.Quantity
	}
	return pos.Quantity
}

func applyBrokerRepair(pos *model.Position, brokerPos ibkr.IBKRPosition) {
	if pos == nil {
		return
	}
	pos.Quantity = math.Abs(brokerPos.Quantity)
	if brokerPos.Quantity < 0 {
		pos.Direction = model.Short
	} else {
		pos.Direction = model.Long
	}
	if brokerPos.AvgCost > 0 {
		pos.EntryPrice = brokerPos.AvgCost
		if pos.CurrentPrice <= 0 {
			pos.CurrentPrice = brokerPos.AvgCost
		}
	}
	if brokerPos.ConID > 0 {
		pos.IBKRContractID = brokerPos.ConID
	}
}

func recoveredBrokerPosition(ip ibkr.IBKRPosition) *model.Position {
	direction := model.Long
	if ip.Quantity < 0 {
		direction = model.Short
	}
	qty := math.Abs(ip.Quantity)
	price := ip.AvgCost
	if price <= 0 {
		price = minShadowEntryPrice
	}
	return &model.Position{
		ID:             "broker-recovered:" + reconcileKey(ip.ConID, ip.Symbol),
		ThesisID:       "",
		DeskID:         brokerRecoveryDeskID,
		Instrument:     model.Instrument{Symbol: ip.Symbol, SecType: ip.SecType, Exchange: ip.Exchange, Currency: ip.Currency, ConID: ip.ConID},
		Direction:      direction,
		Quantity:       qty,
		EntryPrice:     price,
		CurrentPrice:   price,
		IBKRContractID: ip.ConID,
		Status:         "open",
		OpenedAt:       time.Now().UTC(),
	}
}
