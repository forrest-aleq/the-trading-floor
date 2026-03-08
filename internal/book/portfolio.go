package book

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

// Book is the source of truth for portfolio state
type Book struct {
	mu sync.RWMutex
	log *slog.Logger

	positions map[string]*model.Position // position_id → position
	ibkr      *ibkr.Client

	// Aggregates (recalculated on mark)
	nav           float64
	cash          float64
	grossExposure float64
	netExposure   float64
	dailyPnL      float64
	weeklyPnL     float64
	monthlyPnL    float64
	totalPnL      float64
	maxDrawdown   float64
	peakNAV       float64

	// Per-desk tracking
	deskPnL       map[string]float64
	deskPositions map[string]int

	// Config
	initialCapital float64
	reconcileInterval time.Duration
}

func NewBook(ibkrClient *ibkr.Client, initialCapital float64) *Book {
	return &Book{
		log:               slog.Default().With("component", "book"),
		positions:         make(map[string]*model.Position),
		ibkr:              ibkrClient,
		nav:               initialCapital,
		cash:              initialCapital,
		peakNAV:           initialCapital,
		initialCapital:    initialCapital,
		deskPnL:           make(map[string]float64),
		deskPositions:     make(map[string]int),
		reconcileInterval: 60 * time.Second,
	}
}

// OpenPosition records a new position from a fill
func (b *Book) OpenPosition(fill *model.Fill, thesis *model.Thesis) *model.Position {
	b.mu.Lock()
	defer b.mu.Unlock()

	pos := &model.Position{
		ID:             fill.OrderID,
		ThesisID:       thesis.ID,
		DeskID:         thesis.DeskID,
		Instrument:     fill.Instrument,
		Direction:      fill.Direction,
		Quantity:        fill.Quantity,
		EntryPrice:     fill.AvgPrice,
		CurrentPrice:   fill.AvgPrice,
		IBKROrderID:    fill.IBKROrderID,
		Status:         "open",
		OpenedAt:       time.Now(),
	}

	b.positions[pos.ID] = pos
	b.deskPositions[pos.DeskID]++
	b.cash -= fill.AvgPrice * fill.Quantity // Simplified; options have premiums

	b.log.Info("position opened",
		"id", pos.ID,
		"desk", pos.DeskID,
		"symbol", pos.Instrument.Symbol,
		"direction", pos.Direction,
		"qty", pos.Quantity,
		"price", pos.EntryPrice,
	)

	return pos
}

// ClosePosition records a position close
func (b *Book) ClosePosition(positionID string, exitPrice float64, exitReason string) (*model.ThesisOutcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	pos, ok := b.positions[positionID]
	if !ok {
		return nil, nil
	}

	// Calculate P&L
	var pnl float64
	if pos.Direction == model.Long {
		pnl = (exitPrice - pos.EntryPrice) * pos.Quantity
	} else {
		pnl = (pos.EntryPrice - exitPrice) * pos.Quantity
	}

	pos.RealizedPnL = pnl
	pos.CurrentPrice = exitPrice
	pos.Status = "closed"
	now := time.Now()
	pos.ClosedAt = &now

	// Update desk P&L
	b.deskPnL[pos.DeskID] += pnl
	b.deskPositions[pos.DeskID]--
	b.cash += exitPrice * pos.Quantity
	b.dailyPnL += pnl
	b.totalPnL += pnl

	holdingHours := now.Sub(pos.OpenedAt).Hours()

	b.log.Info("position closed",
		"id", pos.ID,
		"desk", pos.DeskID,
		"symbol", pos.Instrument.Symbol,
		"pnl", pnl,
		"reason", exitReason,
		"held_hours", holdingHours,
	)

	return &model.ThesisOutcome{
		Profitable:   pnl > 0,
		RealizedPnL:  pnl,
		ReturnPct:    (pnl / (pos.EntryPrice * pos.Quantity)) * 100,
		HoldingHours: holdingHours,
		ExitReason:   exitReason,
	}, nil
}

// Mark updates all position prices and recalculates aggregates
func (b *Book) Mark(prices map[string]float64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.grossExposure = 0
	b.netExposure = 0
	totalUnrealized := 0.0

	for _, pos := range b.positions {
		if pos.Status != "open" {
			continue
		}

		price, ok := prices[pos.Instrument.Symbol]
		if !ok {
			continue
		}

		pos.CurrentPrice = price

		var unrealized float64
		notional := price * pos.Quantity
		if pos.Direction == model.Long {
			unrealized = (price - pos.EntryPrice) * pos.Quantity
			b.netExposure += notional
		} else {
			unrealized = (pos.EntryPrice - price) * pos.Quantity
			b.netExposure -= notional
		}
		pos.UnrealizedPnL = unrealized
		totalUnrealized += unrealized
		b.grossExposure += notional
	}

	b.nav = b.cash + totalUnrealized
	if b.nav > b.peakNAV {
		b.peakNAV = b.nav
	}
	drawdown := (b.peakNAV - b.nav) / b.peakNAV
	if drawdown > b.maxDrawdown {
		b.maxDrawdown = drawdown
	}
}

// Snapshot returns current portfolio state for risk checks
func (b *Book) Snapshot() PortfolioSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()

	openCount := 0
	for _, pos := range b.positions {
		if pos.Status == "open" {
			openCount++
		}
	}

	// Copy desk maps
	deskPnL := make(map[string]float64)
	deskPos := make(map[string]int)
	deskCap := make(map[string]float64)
	for k, v := range b.deskPnL {
		deskPnL[k] = v
	}
	for k, v := range b.deskPositions {
		deskPos[k] = v
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
}

// StartReconcile runs periodic reconciliation against IBKR
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

func (b *Book) reconcile(ctx context.Context) {
	ibkrPositions, err := b.ibkr.GetPositions(ctx)
	if err != nil {
		b.log.Error("reconciliation failed", "error", err)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Build map of IBKR positions by contract ID
	ibkrMap := make(map[int64]ibkr.IBKRPosition)
	for _, p := range ibkrPositions {
		ibkrMap[p.ConID] = p
	}

	// Check our positions against IBKR
	discrepancies := 0
	for _, pos := range b.positions {
		if pos.Status != "open" {
			continue
		}
		ibkrPos, exists := ibkrMap[pos.IBKRContractID]
		if !exists {
			b.log.Warn("position exists locally but not in IBKR",
				"symbol", pos.Instrument.Symbol,
				"qty", pos.Quantity,
			)
			discrepancies++
			continue
		}
		if ibkrPos.Quantity != pos.Quantity {
			b.log.Warn("quantity mismatch",
				"symbol", pos.Instrument.Symbol,
				"local", pos.Quantity,
				"ibkr", ibkrPos.Quantity,
			)
			// IBKR is source of truth
			pos.Quantity = ibkrPos.Quantity
			discrepancies++
		}
	}

	if discrepancies > 0 {
		b.log.Warn("reconciliation complete", "discrepancies", discrepancies)
	}
}

// GetOpenPositions returns all open positions
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

// ResetDaily resets daily P&L counters (call at market open)
func (b *Book) ResetDaily() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dailyPnL = 0
	for k := range b.deskPnL {
		b.deskPnL[k] = 0
	}
}
