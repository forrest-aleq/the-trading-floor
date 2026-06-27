package book

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
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
	now               func() time.Time

	// Calendar anchors for honest period P&L. Daily realized P&L resets on
	// date change; weekly/monthly P&L measure NAV against the NAV captured at
	// the period boundary. In-memory only: a mid-period restart re-anchors at
	// initial capital, so period figures span at most since-restart.
	loc            *time.Location
	dayKey         string
	weekKey        string
	monthKey       string
	weekAnchorNAV  float64
	monthAnchorNAV float64
}

type brokerAccountState struct {
	connected           bool
	nav                 float64
	cash                float64
	buyingPower         float64
	equityWithLoanValue float64
	grossPositionValue  float64
	regTEquity          float64
	regTMargin          float64
	sma                 float64
	initMarginReq       float64
	maintMarginReq      float64
	availableFunds      float64
	excessLiquidity     float64
	dailyPnL            float64
	dailyPnLAvailable   bool
	dailyPnLSource      string
	unrealizedPnL       float64
	realizedPnL         float64
	openPositions       int
	grossExposure       float64
	netExposure         float64
	lastSynced          time.Time
	lastAccountSynced   time.Time
	lastPnLSynced       time.Time
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
	BuyingPower         float64
	EquityWithLoanValue float64
	GrossPositionValue  float64
	RegTEquity          float64
	RegTMargin          float64
	SMA                 float64
	InitMarginReq       float64
	MaintMarginReq      float64
	AvailableFunds      float64
	ExcessLiquidity     float64
	DailyPnL            float64
	DailyPnLAvailable   bool
	DailyPnLSource      string
	UnrealizedPnL       float64
	RealizedPnL         float64
	OpenPositions       int
	GrossExposure       float64
	NetExposure         float64
	LastSynced          time.Time
	LastAccountSynced   time.Time
	LastPnLSynced       time.Time
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
	log := slog.Default().With("component", "book")
	loc := rolloverLocation(log)
	day, week, month := periodKeys(time.Now(), loc)
	return &Book{
		log:               log,
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
		loc:               loc,
		dayKey:            day,
		weekKey:           week,
		monthKey:          month,
		weekAnchorNAV:     initialCapital,
		monthAnchorNAV:    initialCapital,
	}
}

func (b *Book) currentTime() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Trading days roll on the exchange calendar, not the host clock: a UTC
// server must not reset daily P&L at 8pm New York time.
func rolloverLocation(log *slog.Logger) *time.Location {
	name := strings.TrimSpace(os.Getenv("BOOK_ROLLOVER_TZ"))
	if name == "" {
		name = "America/New_York"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Warn("failed to load rollover timezone; falling back to UTC", "tz", name, "error", err)
		return time.UTC
	}
	return loc
}

func periodKeys(t time.Time, loc *time.Location) (day, week, month string) {
	if loc != nil {
		t = t.In(loc)
	}
	isoYear, isoWeek := t.ISOWeek()
	return t.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", isoYear, isoWeek), t.Format("2006-01")
}

// rolloverLocked must run before NAV is recalculated so period anchors
// capture the NAV the portfolio carried into the new period.
func (b *Book) rolloverLocked(now time.Time) {
	day, week, month := periodKeys(now, b.loc)
	if day != b.dayKey {
		b.dayKey = day
		b.dailyPnL = 0
		for k := range b.deskPnL {
			b.deskPnL[k] = 0
		}
	}
	if week != b.weekKey {
		b.weekKey = week
		b.weekAnchorNAV = b.nav
	}
	if month != b.monthKey {
		b.monthKey = month
		b.monthAnchorNAV = b.nav
	}
}

func (b *Book) SetDeskCapital(deskID string, capital float64) {
	b.mu.Lock()
	b.deskCapital[deskID] = capital
	b.mu.Unlock()
}

func (b *Book) HydrateOpenPositions(positions []*model.Position) int {
	if b == nil || len(positions) == 0 {
		return 0
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	count := 0
	for _, source := range positions {
		if source == nil || source.ID == "" || source.Status != "open" {
			continue
		}
		if _, exists := b.positions[source.ID]; exists {
			continue
		}

		pos := cloneBookPosition(source)
		if pos.CurrentPrice <= 0 {
			pos.CurrentPrice = pos.EntryPrice
		}
		if pos.OpenedAt.IsZero() {
			pos.OpenedAt = b.currentTime()
		}
		b.positions[pos.ID] = pos
		if !pos.Shadow {
			b.deskPositions[pos.DeskID]++
			b.totalTrades++
			notional := positionCashNotional(pos, pos.EntryPrice)
			if pos.Direction == model.Long {
				b.cash -= notional
			} else {
				b.cash += notional
			}
		}
		count++
	}
	if count > 0 {
		b.recalculateLocked()
		b.log.Info("open positions hydrated", "positions", count)
	}
	return count
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

func cloneBookPosition(pos *model.Position) *model.Position {
	if pos == nil {
		return nil
	}
	cloned := *pos
	if len(pos.Legs) > 0 {
		cloned.Legs = append([]model.TradeLeg(nil), pos.Legs...)
	}
	if pos.ClosedAt != nil {
		closedAt := *pos.ClosedAt
		cloned.ClosedAt = &closedAt
	}
	return &cloned
}

func (b *Book) ApplyExecutionFill(fill *model.Fill, thesis *model.Thesis) *model.Position {
	if fill == nil || thesis == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if existing, ok := b.positions[fill.OrderID]; ok && existing != nil && existing.Status == "open" && !existing.Shadow {
		b.applyExecutionFillLocked(existing, fill)
		b.recalculateLocked()
		return existing
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
	pos.MarkedAt = pos.OpenedAt
	return pos
}

func (b *Book) applyExecutionFillLocked(pos *model.Position, fill *model.Fill) {
	if pos == nil || fill == nil {
		return
	}

	previousNotional := positionCashNotional(pos, pos.EntryPrice)
	if fill.Direction != "" {
		pos.Direction = fill.Direction
	}
	if fill.Quantity > 0 {
		pos.Quantity = fill.Quantity
	}
	if fill.AvgPrice > 0 {
		pos.EntryPrice = fill.AvgPrice
		if pos.CurrentPrice <= 0 || pos.CurrentPrice == minShadowEntryPrice {
			pos.CurrentPrice = fill.AvgPrice
			pos.MarkedAt = fill.FilledAt
			if pos.MarkedAt.IsZero() {
				pos.MarkedAt = b.currentTime()
			}
		}
	}
	if fill.IBKROrderID > 0 {
		pos.IBKROrderID = fill.IBKROrderID
	}
	if inst := fill.PrimaryInstrument(); inst.Symbol != "" {
		pos.Instrument = inst
	}
	if len(fill.Legs) > 0 {
		pos.Legs = append([]model.TradeLeg(nil), fill.Legs...)
	}
	if !fill.FilledAt.IsZero() {
		if pos.OpenedAt.IsZero() || fill.FilledAt.Before(pos.OpenedAt) {
			pos.OpenedAt = fill.FilledAt
		}
	}

	updatedNotional := positionCashNotional(pos, pos.EntryPrice)
	delta := updatedNotional - previousNotional
	if delta == 0 {
		return
	}
	if pos.Direction == model.Long {
		b.cash -= delta
	} else {
		b.cash += delta
	}
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

	markTime := b.currentTime()
	for _, pos := range b.positions {
		if pos.Status != "open" {
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
				pos.MarkedAt = markTime
			}
			continue
		}

		if price, ok := lookupInstrumentPrice(prices, pos.Instrument); ok && price > 0 {
			pos.CurrentPrice = price
			pos.MarkedAt = markTime
		}
	}

	b.recalculateLocked()
}

func (b *Book) Snapshot() PortfolioSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	// A quiet book may cross a period boundary with no fills or marks to
	// trigger a recalculation; observing the calendar here keeps reported
	// period P&L honest regardless of activity. Period P&L is measured on
	// the local book's NAV stream (anchors and deltas alike) even when the
	// snapshot's NAV column reports broker equity.
	b.rolloverLocked(b.currentTime())
	b.weeklyPnL = b.nav - b.weekAnchorNAV
	b.monthlyPnL = b.nav - b.monthAnchorNAV

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
	dailyPnLAvailable := true
	dailyPnLSource := "book"
	grossExposure := b.grossExposure
	netExposure := b.netExposure
	if b.brokerSync.connected && b.brokerSync.nav > 0 {
		nav = b.brokerSync.nav
		cash = b.brokerSync.cash
		dailyPnLAvailable = b.brokerSync.dailyPnLAvailable
		dailyPnLSource = b.brokerSync.dailyPnLSource
		if dailyPnLAvailable {
			dailyPnL = b.brokerSync.dailyPnL
		} else {
			dailyPnL = 0
		}
		grossExposure = b.brokerSync.grossExposure
		netExposure = b.brokerSync.netExposure
		openCount = b.brokerSync.openPositions
	}

	peak := b.peakNAV
	if nav > peak {
		peak = nav
	}
	currentDrawdownPct := 0.0
	if peak > 0 && nav < peak {
		currentDrawdownPct = (peak - nav) / peak * 100
	}

	return PortfolioSnapshot{
		NAV:                nav,
		Cash:               cash,
		GrossExposure:      grossExposure,
		NetExposure:        netExposure,
		DailyPnL:           dailyPnL,
		DailyPnLAvailable:  dailyPnLAvailable,
		DailyPnLSource:     dailyPnLSource,
		WeeklyPnL:          b.weeklyPnL,
		MonthlyPnL:         b.monthlyPnL,
		TotalPnL:           b.totalPnL,
		MaxDrawdown:        b.maxDrawdown,
		CurrentDrawdownPct: currentDrawdownPct,
		OpenPositions:      openCount,
		DeskPnL:            deskPnL,
		DeskPositions:      deskPos,
		DeskCapital:        deskCap,
		TotalTrades:        b.totalTrades,
	}
}

func (b *Book) BrokerSyncStatus() BrokerSyncStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return BrokerSyncStatus{
		Connected:           b.brokerSync.connected,
		NAV:                 b.brokerSync.nav,
		Cash:                b.brokerSync.cash,
		BuyingPower:         b.brokerSync.buyingPower,
		EquityWithLoanValue: b.brokerSync.equityWithLoanValue,
		GrossPositionValue:  b.brokerSync.grossPositionValue,
		RegTEquity:          b.brokerSync.regTEquity,
		RegTMargin:          b.brokerSync.regTMargin,
		SMA:                 b.brokerSync.sma,
		InitMarginReq:       b.brokerSync.initMarginReq,
		MaintMarginReq:      b.brokerSync.maintMarginReq,
		AvailableFunds:      b.brokerSync.availableFunds,
		ExcessLiquidity:     b.brokerSync.excessLiquidity,
		DailyPnL:            b.brokerSync.dailyPnL,
		DailyPnLAvailable:   b.brokerSync.dailyPnLAvailable,
		DailyPnLSource:      b.brokerSync.dailyPnLSource,
		UnrealizedPnL:       b.brokerSync.unrealizedPnL,
		RealizedPnL:         b.brokerSync.realizedPnL,
		OpenPositions:       b.brokerSync.openPositions,
		GrossExposure:       b.brokerSync.grossExposure,
		NetExposure:         b.brokerSync.netExposure,
		LastSynced:          b.brokerSync.lastSynced,
		LastAccountSynced:   b.brokerSync.lastAccountSynced,
		LastPnLSynced:       b.brokerSync.lastPnLSynced,
		LastPositionsSynced: b.brokerSync.lastPositionsSynced,
		LastFailure:         b.brokerSync.lastFailure,
		LastError:           b.brokerSync.lastError,
		ConsecutiveFailures: b.brokerSync.consecutiveFailures,
	}
}

type PortfolioSnapshot struct {
	NAV               float64
	Cash              float64
	GrossExposure     float64
	NetExposure       float64
	DailyPnL          float64
	DailyPnLAvailable bool
	DailyPnLSource    string
	WeeklyPnL         float64
	MonthlyPnL        float64
	TotalPnL          float64
	// MaxDrawdown is the high-water drawdown fraction for the run;
	// CurrentDrawdownPct is the live percentage off peak NAV.
	MaxDrawdown        float64
	CurrentDrawdownPct float64
	OpenPositions      int
	DeskPnL            map[string]float64
	DeskPositions      map[string]int
	DeskCapital        map[string]float64
	TotalTrades        int64
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

	bookByConID := make(map[string][]*model.Position)
	bookBySymbol := make(map[string][]*model.Position)
	for _, pos := range b.positions {
		if pos.Status != "open" || pos.Shadow {
			continue
		}
		if pos.IBKRContractID > 0 {
			key := reconcileKey(pos.IBKRContractID, pos.Instrument.Symbol)
			bookByConID[key] = append(bookByConID[key], pos)
		}
		symbolKey := reconcileSymbolKey(pos.Instrument.Symbol)
		bookBySymbol[symbolKey] = append(bookBySymbol[symbolKey], pos)
	}

	seen := make(map[string]bool)
	seenPositions := make(map[*model.Position]bool)
	var discrepancies []Discrepancy

	for _, ip := range ibkrPositions {
		key, positions := reconcileGroup(ip, bookByConID, bookBySymbol)
		seen[key] = true
		for _, pos := range positions {
			seenPositions[pos] = true
		}

		if len(positions) == 0 {
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

		bookQty := aggregateSignedQuantity(positions)
		bookAvg := aggregateEntryPrice(positions)
		brokerAvg := normalizedBrokerAvgCost(positions[0], ip.AvgCost)
		if !approxEqual(bookQty, ip.Quantity) || !approxPriceEqual(bookAvg, brokerAvg) {
			if len(positions) == 1 && positions[0].DeskID != brokerRecoveryDeskID {
				applyBrokerRepair(positions[0], ip, brokerAvg)
			} else {
				b.applyBrokerRecoveryDeltaLocked(key, positions, ip, brokerAvg)
			}
			discrepancies = append(discrepancies, Discrepancy{
				Symbol:      ip.Symbol,
				BookQty:     bookQty,
				IBKRQty:     ip.Quantity,
				BookAvgCost: bookAvg,
				IBKRAvgCost: brokerAvg,
			})
		}
	}

	for key, positions := range bookByConID {
		if seen[key] {
			continue
		}
		positions = unseenReconcilePositions(positions, seenPositions)
		if len(positions) == 0 {
			continue
		}
		for _, pos := range positions {
			seen[reconcileSymbolKey(pos.Instrument.Symbol)] = true
		}
		bookQty := aggregateSignedQuantity(positions)
		bookAvg := aggregateEntryPrice(positions)
		discrepancies = append(discrepancies, Discrepancy{
			Symbol:      positions[0].DisplaySymbol(),
			BookQty:     bookQty,
			IBKRQty:     0,
			BookAvgCost: bookAvg,
			IBKRAvgCost: 0,
		})
	}
	for key, positions := range bookBySymbol {
		if seen[key] {
			continue
		}
		positions = unseenReconcilePositions(positions, seenPositions)
		if len(positions) == 0 {
			continue
		}
		bookQty := aggregateSignedQuantity(positions)
		bookAvg := aggregateEntryPrice(positions)
		discrepancies = append(discrepancies, Discrepancy{
			Symbol:      positions[0].DisplaySymbol(),
			BookQty:     bookQty,
			IBKRQty:     0,
			BookAvgCost: bookAvg,
			IBKRAvgCost: 0,
		})
	}

	b.recalculateLocked()
	return discrepancies
}

// approxEqual tolerates float64 representation noise without masking real
// quantity or price drift between the book and the broker.
func approxEqual(a, b float64) bool {
	diff := math.Abs(a - b)
	if diff <= 1e-6 {
		return true
	}
	return diff <= math.Max(math.Abs(a), math.Abs(b))*1e-9
}

func approxPriceEqual(a, b float64) bool {
	if approxEqual(a, b) {
		return true
	}
	return math.Abs(a-b) <= 0.01
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
	b.rolloverLocked(b.currentTime())

	totalEquityAdjustment := 0.0
	totalUnrealized := 0.0
	b.grossExposure = 0
	b.netExposure = 0

	for _, pos := range b.positions {
		if pos.Status != "open" {
			continue
		}

		if pos.Shadow {
			pos.UnrealizedPnL = positionUnrealizedPnL(pos)
			continue
		}

		marketValue := positionNetMarketValue(pos)
		grossValue := positionGrossExposure(pos)
		if pos.Direction == model.Long {
			totalEquityAdjustment += marketValue
			b.netExposure += marketValue
		} else {
			totalEquityAdjustment -= marketValue
			b.netExposure -= marketValue
		}
		pos.UnrealizedPnL = positionUnrealizedPnL(pos)
		b.grossExposure += grossValue
		totalUnrealized += pos.UnrealizedPnL
	}

	b.nav = b.cash + totalEquityAdjustment
	b.totalPnL = b.nav - b.initialCapital
	b.weeklyPnL = b.nav - b.weekAnchorNAV
	b.monthlyPnL = b.nav - b.monthAnchorNAV

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

func positionUnrealizedPnL(pos *model.Position) float64 {
	if pos == nil {
		return 0
	}
	multiplier := pos.PrimaryInstrument().MultiplierValue()
	if pos.Direction == model.Long {
		return (pos.CurrentPrice - pos.EntryPrice) * pos.Quantity * multiplier
	}
	return (pos.EntryPrice - pos.CurrentPrice) * pos.Quantity * multiplier
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

	if b.positionSource == nil {
		if accountHealthy {
			b.markBrokerSyncHealthy()
		}
		return
	}

	ibkrPositions, err := b.positionSource.GetPositions(ctx)
	if err != nil {
		b.recordBrokerSyncFailure("positions", err)
		return
	}

	b.applyBrokerPositions(ibkrPositions)
	if accountHealthy {
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
		// Broker equity is the authoritative NAV stream; peak and max
		// drawdown must see it so the kill switch reflects account reality
		// even when the local book is quiet or out of sync.
		if summary.NetLiquidation > b.peakNAV {
			b.peakNAV = summary.NetLiquidation
		}
		if b.peakNAV > 0 {
			if drawdown := (b.peakNAV - summary.NetLiquidation) / b.peakNAV; drawdown > b.maxDrawdown {
				b.maxDrawdown = drawdown
			}
		}
	}
	b.brokerSync.cash = summary.Cash
	b.brokerSync.buyingPower = summary.BuyingPower
	b.brokerSync.equityWithLoanValue = summary.EquityWithLoanValue
	b.brokerSync.grossPositionValue = summary.GrossPositionValue
	b.brokerSync.regTEquity = summary.RegTEquity
	b.brokerSync.regTMargin = summary.RegTMargin
	b.brokerSync.sma = summary.SMA
	b.brokerSync.initMarginReq = summary.InitMarginReq
	b.brokerSync.maintMarginReq = summary.MaintMarginReq
	b.brokerSync.availableFunds = summary.AvailableFunds
	b.brokerSync.excessLiquidity = summary.ExcessLiquidity
	if summary.DailyPnLReady {
		b.brokerSync.dailyPnL = summary.DailyPnL
		b.brokerSync.dailyPnLAvailable = true
		b.brokerSync.dailyPnLSource = summary.PnLSource
		b.brokerSync.unrealizedPnL = summary.UnrealizedPnL
		b.brokerSync.realizedPnL = summary.RealizedPnL
		b.brokerSync.lastPnLSynced = time.Now()
	} else if b.brokerSync.lastPnLSynced.IsZero() {
		b.brokerSync.dailyPnL = 0
		b.brokerSync.dailyPnLAvailable = false
		b.brokerSync.dailyPnLSource = ""
	}
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
	return reconcileSymbolKey(symbol)
}

func reconcileSymbolKey(symbol string) string {
	return "symbol:" + strings.ToUpper(strings.TrimSpace(symbol))
}

func reconcileGroup(ip ibkr.IBKRPosition, byConID map[string][]*model.Position, bySymbol map[string][]*model.Position) (string, []*model.Position) {
	conIDKey := ""
	var conIDGroup []*model.Position
	if ip.ConID > 0 {
		conIDKey = reconcileKey(ip.ConID, ip.Symbol)
		conIDGroup = byConID[conIDKey]
	}

	symbolKey := reconcileSymbolKey(ip.Symbol)
	symbolGroup := bySymbol[symbolKey]
	if len(symbolGroup) > len(conIDGroup) {
		return symbolKey, symbolGroup
	}
	if len(conIDGroup) > 0 {
		return conIDKey, conIDGroup
	}
	if len(symbolGroup) > 0 {
		return symbolKey, symbolGroup
	}
	return reconcileKey(ip.ConID, ip.Symbol), nil
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

func aggregateSignedQuantity(positions []*model.Position) float64 {
	total := 0.0
	for _, pos := range positions {
		total += signedPositionQuantity(pos)
	}
	return total
}

func aggregateEntryPrice(positions []*model.Position) float64 {
	weighted := 0.0
	quantity := 0.0
	for _, pos := range positions {
		if pos == nil || pos.Quantity <= 0 {
			continue
		}
		qty := math.Abs(pos.Quantity)
		weighted += pos.EntryPrice * qty
		quantity += qty
	}
	if quantity <= 0 {
		return 0
	}
	return weighted / quantity
}

func unseenReconcilePositions(positions []*model.Position, seen map[*model.Position]bool) []*model.Position {
	if len(positions) == 0 || len(seen) == 0 {
		return positions
	}
	filtered := positions[:0]
	for _, pos := range positions {
		if pos != nil && !seen[pos] {
			filtered = append(filtered, pos)
		}
	}
	return filtered
}

func firstNonEmptyBookString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// normalizedBrokerAvgCost converts IBKR's avgCost to the book's per-share
// convention. IBKR reports option avgCost per contract (premium × multiplier,
// including fees) while the book stores per-share premiums; positionUnrealizedPnL
// re-applies the multiplier, so storing a per-contract cost corrupts P&L and
// stops by ~100x. Accept either convention from upstream by picking the
// interpretation closest to the position's own price.
func normalizedBrokerAvgCost(pos *model.Position, avgCost float64) float64 {
	if pos == nil || avgCost <= 0 {
		return avgCost
	}
	// FOP multipliers vary per contract (ES=50, CL=1000, ...), so they
	// normalize only when the instrument carries an explicit Multiplier —
	// desk-originated FOP always does; broker-recovered FOP cannot be
	// normalized and keeps the raw broker value.
	mult := pos.PrimaryInstrument().MultiplierValue()
	if mult <= 1 {
		return avgCost
	}
	perShare := avgCost / mult
	ref := pos.CurrentPrice
	if ref <= 0 {
		ref = pos.EntryPrice
	}
	if ref <= 0 {
		return perShare
	}
	if math.Abs(perShare-ref) <= math.Abs(avgCost-ref) {
		return perShare
	}
	return avgCost
}

func applyBrokerRepair(pos *model.Position, brokerPos ibkr.IBKRPosition, avgCost float64) {
	if pos == nil {
		return
	}
	pos.Quantity = math.Abs(brokerPos.Quantity)
	if brokerPos.Quantity < 0 {
		pos.Direction = model.Short
	} else {
		pos.Direction = model.Long
	}
	if avgCost > 0 {
		pos.EntryPrice = avgCost
		if pos.CurrentPrice <= 0 {
			pos.CurrentPrice = avgCost
		}
	}
	if brokerPos.ConID > 0 {
		pos.IBKRContractID = brokerPos.ConID
	}
}

func (b *Book) applyBrokerRecoveryDeltaLocked(key string, positions []*model.Position, brokerPos ibkr.IBKRPosition, avgCost float64) {
	attributedQty := 0.0
	var recovery *model.Position
	for _, pos := range positions {
		if pos == nil {
			continue
		}
		if pos.DeskID == brokerRecoveryDeskID || strings.HasPrefix(pos.ID, "broker-recovered:") {
			if recovery == nil {
				recovery = pos
			}
			continue
		}
		attributedQty += signedPositionQuantity(pos)
	}

	delta := brokerPos.Quantity - attributedQty
	if approxEqual(delta, 0) {
		if recovery != nil {
			now := b.currentTime()
			recovery.Quantity = 0
			recovery.Status = "closed"
			recovery.ClosedAt = &now
		}
		return
	}

	if recovery == nil {
		recovery = recoveredBrokerPosition(brokerPos)
		recovery.ID = "broker-recovered:" + key
		b.positions[recovery.ID] = recovery
		if !recovery.Shadow {
			b.deskPositions[recovery.DeskID]++
		}
	}

	recovery.Status = "open"
	recovery.ClosedAt = nil
	recovery.Direction = model.Long
	if delta < 0 {
		recovery.Direction = model.Short
	}
	recovery.Quantity = math.Abs(delta)
	if avgCost > 0 {
		recovery.EntryPrice = avgCost
		if recovery.CurrentPrice <= 0 {
			recovery.CurrentPrice = avgCost
		}
	}
	recoveredInst := recoveredBrokerInstrument(brokerPos)
	recovery.Instrument.Symbol = firstNonEmptyBookString(recovery.Instrument.Symbol, recoveredInst.Symbol)
	recovery.Instrument.SecType = firstNonEmptyBookString(recovery.Instrument.SecType, recoveredInst.SecType)
	if strings.TrimSpace(recoveredInst.Exchange) != "" {
		recovery.Instrument.Exchange = recoveredInst.Exchange
	}
	recovery.Instrument.Currency = firstNonEmptyBookString(recovery.Instrument.Currency, recoveredInst.Currency)
	if brokerPos.ConID > 0 {
		recovery.Instrument.ConID = brokerPos.ConID
		recovery.IBKRContractID = brokerPos.ConID
	}
}

func recoveredBrokerPosition(ip ibkr.IBKRPosition) *model.Position {
	direction := model.Long
	if ip.Quantity < 0 {
		direction = model.Short
	}
	qty := math.Abs(ip.Quantity)
	inst := recoveredBrokerInstrument(ip)
	price := ip.AvgCost
	if mult := inst.MultiplierValue(); mult > 1 && price > 0 {
		price /= mult
	}
	if price <= 0 {
		price = minShadowEntryPrice
	}
	return &model.Position{
		ID:             "broker-recovered:" + reconcileKey(ip.ConID, ip.Symbol),
		ThesisID:       "",
		DeskID:         brokerRecoveryDeskID,
		Instrument:     inst,
		Direction:      direction,
		Quantity:       qty,
		EntryPrice:     price,
		CurrentPrice:   price,
		IBKRContractID: ip.ConID,
		Status:         "open",
		OpenedAt:       time.Now().UTC(),
	}
}

func recoveredBrokerInstrument(ip ibkr.IBKRPosition) model.Instrument {
	inst := model.Instrument{
		Symbol:   strings.TrimSpace(ip.Symbol),
		SecType:  strings.ToUpper(strings.TrimSpace(ip.SecType)),
		Exchange: strings.ToUpper(strings.TrimSpace(ip.Exchange)),
		Currency: strings.ToUpper(strings.TrimSpace(ip.Currency)),
		ConID:    ip.ConID,
	}
	if inst.SecType == "ETF" {
		inst.SecType = "STK"
	}
	if inst.SecType != "" && inst.SecType != "STK" {
		return inst
	}
	if isDirectUSStockExchange(inst.Exchange) && (inst.Currency == "" || inst.Currency == "USD") {
		inst.Exchange = "SMART"
		if inst.Currency == "" {
			inst.Currency = "USD"
		}
	}
	return inst
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
