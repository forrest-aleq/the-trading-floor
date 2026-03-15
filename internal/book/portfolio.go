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

// Book is the source of truth for portfolio state.
type Book struct {
	mu  sync.RWMutex
	log *slog.Logger

	positions         map[string]*model.Position // position_id -> position
	positionSource    PositionSource
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
}

type Discrepancy struct {
	Symbol      string
	BookQty     float64
	IBKRQty     float64
	BookAvgCost float64
	IBKRAvgCost float64
}

func NewBook(positionSource PositionSource, initialCapital float64) *Book {
	return &Book{
		log:               slog.Default().With("component", "book"),
		positions:         make(map[string]*model.Position),
		positionSource:    positionSource,
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

	fill := &model.Fill{
		OrderID:    thesis.ID,
		Instrument: thesis.Instrument,
		Direction:  thesis.Direction,
		Quantity:   thesis.PositionSize,
		AvgPrice:   thesis.EntryPrice,
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
	legs := make([]model.TradeLeg, 0)
	switch {
	case fill != nil && len(fill.Legs) > 0:
		legs = append(legs, fill.Legs...)
	case thesis != nil && len(thesis.Legs) > 0:
		legs = append(legs, thesis.Legs...)
	}
	for i := range legs {
		if legs[i].EntryPrice <= 0 {
			legs[i].EntryPrice = fill.AvgPrice
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
		EntryPrice:     fill.AvgPrice,
		CurrentPrice:   fill.AvgPrice,
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

	return PortfolioSnapshot{
		NAV:           b.nav,
		Cash:          b.cash,
		GrossExposure: b.grossExposure,
		NetExposure:   b.netExposure,
		DailyPnL:      b.dailyPnL,
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
	b.mu.RLock()
	defer b.mu.RUnlock()

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
			discrepancies = append(discrepancies, Discrepancy{
				Symbol:      ip.Symbol,
				BookQty:     0,
				IBKRQty:     ip.Quantity,
				BookAvgCost: 0,
				IBKRAvgCost: ip.AvgCost,
			})
			continue
		}

		if pos.Quantity != ip.Quantity || pos.EntryPrice != ip.AvgCost {
			discrepancies = append(discrepancies, Discrepancy{
				Symbol:      ip.Symbol,
				BookQty:     pos.Quantity,
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
	if b.positionSource == nil {
		return
	}

	ibkrPositions, err := b.positionSource.GetPositions(ctx)
	if err != nil {
		b.log.Error("reconciliation failed", "error", err)
		return
	}

	discrepancies := b.Reconcile(ibkrPositions)
	if len(discrepancies) == 0 {
		return
	}

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
