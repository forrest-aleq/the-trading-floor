package execution

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

type OrderState string

const (
	OrderStateWorking         OrderState = "working"
	OrderStatePartiallyFilled OrderState = "partially_filled"
	OrderStateFilled          OrderState = "filled"
	OrderStateCancelled       OrderState = "cancelled"
	OrderStateFailed          OrderState = "failed"
)

type ExecutionQuality struct {
	DecisionPrice              float64 `json:"decision_price,omitempty"`
	ReferencePrice             float64 `json:"reference_price,omitempty"`
	WorkingAgeSeconds          float64 `json:"working_age_seconds,omitempty"`
	FillRatio                  float64 `json:"fill_ratio,omitempty"`
	ImplementationShortfall    float64 `json:"implementation_shortfall,omitempty"`
	ImplementationShortfallBps float64 `json:"implementation_shortfall_bps,omitempty"`
	ReferenceShortfall         float64 `json:"reference_shortfall,omitempty"`
	ReferenceShortfallBps      float64 `json:"reference_shortfall_bps,omitempty"`
}

type OrderSnapshot struct {
	OrderID           string
	ThesisID          string
	DeskID            string
	DisplaySymbol     string
	BrokerOrderID     int64
	State             OrderState
	BrokerStatus      string
	Quantity          float64
	FilledQuantity    float64
	RemainingQuantity float64
	AvgFillPrice      float64
	LastFillPrice     float64
	SubmittedAt       time.Time
	UpdatedAt         time.Time
	LastError         string
	Paper             bool
	ExecutionQuality  ExecutionQuality
}

type OrderUpdate struct {
	Snapshot OrderSnapshot
	Fill     *model.Fill
}

type trackedOrder struct {
	order    model.Order
	snapshot OrderSnapshot
	fill     *model.Fill
}

func (s OrderSnapshot) IsWorking() bool {
	return s.State == OrderStateWorking || s.State == OrderStatePartiallyFilled
}

func (m *Manager) WorkingOrders() []OrderSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked(time.Now().UTC())
	working := make([]OrderSnapshot, 0, len(m.tracked))
	for _, tracked := range m.tracked {
		if tracked.snapshot.IsWorking() {
			working = append(working, tracked.snapshot)
		}
	}
	sort.Slice(working, func(i, j int) bool {
		if working[i].SubmittedAt.Equal(working[j].SubmittedAt) {
			return working[i].OrderID < working[j].OrderID
		}
		return working[i].SubmittedAt.Before(working[j].SubmittedAt)
	})
	return working
}

func (m *Manager) OrderStatus(orderID string) (OrderSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked(time.Now().UTC())
	tracked, ok := m.tracked[orderID]
	if !ok {
		return OrderSnapshot{}, false
	}
	return tracked.snapshot, true
}

func (m *Manager) RefreshWorkingOrders(ctx context.Context) []OrderUpdate {
	m.mu.Lock()
	m.pruneExpiredLocked(time.Now().UTC())
	pending := make([]trackedOrder, 0, len(m.tracked))
	for _, tracked := range m.tracked {
		if tracked.snapshot.IsWorking() && tracked.snapshot.BrokerOrderID > 0 {
			pending = append(pending, *tracked)
		}
	}
	m.mu.Unlock()

	updates := make([]OrderUpdate, 0, len(pending))
	for _, tracked := range pending {
		lookup, err := m.ibkr.GetOrderStatus(ctx, tracked.order, tracked.snapshot.BrokerOrderID)
		if err != nil {
			m.log.Warn("working order refresh failed",
				"order_id", tracked.snapshot.OrderID,
				"broker_order_id", tracked.snapshot.BrokerOrderID,
				"error", err,
			)
			continue
		}
		if lookup == nil {
			continue
		}

		update, changed := m.applyOrderLookup(tracked.order, *lookup)
		if changed {
			updates = append(updates, update)
		}
	}
	return updates
}

func (m *Manager) CancelWorkingOrder(ctx context.Context, orderID string) error {
	m.mu.Lock()
	tracked, ok := m.tracked[orderID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("order %s not tracked", orderID)
	}
	snapshot := tracked.snapshot
	m.mu.Unlock()

	if snapshot.BrokerOrderID <= 0 {
		return fmt.Errorf("order %s has no broker order id", orderID)
	}
	if !snapshot.IsWorking() {
		return fmt.Errorf("order %s is not working", orderID)
	}
	if err := m.ibkr.CancelOrder(ctx, snapshot.BrokerOrderID); err != nil {
		return err
	}

	now := time.Now().UTC()
	m.mu.Lock()
	current, ok := m.tracked[orderID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	current.snapshot.BrokerStatus = "cancel_requested"
	current.snapshot.UpdatedAt = now
	current.snapshot.LastError = ""
	record := persistedOrderFromTracked(current)
	m.mu.Unlock()
	m.persistTrackedOrder(record)
	return nil
}

func (m *Manager) applyOrderLookup(order model.Order, lookup ibkr.OrderLookup) (OrderUpdate, bool) {
	now := lookup.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	m.mu.Lock()

	tracked, ok := m.tracked[order.ID]
	if !ok {
		m.mu.Unlock()
		return OrderUpdate{}, false
	}

	prev := tracked.snapshot
	next := prev
	next.BrokerOrderID = lookup.OrderID
	next.BrokerStatus = lookup.Status
	next.FilledQuantity = lookup.FilledQuantity
	next.RemainingQuantity = lookup.RemainingQuantity
	next.AvgFillPrice = lookup.AvgFillPrice
	next.LastFillPrice = lookup.LastFillPrice
	next.UpdatedAt = now
	next.LastError = ""

	fill := cloneFill(lookup.Fill)
	switch {
	case fill != nil:
		next.State = OrderStateFilled
		next.AvgFillPrice = fill.AvgPrice
		next.FilledQuantity = fill.Quantity
		next.RemainingQuantity = 0
		tracked.fill = fill
		m.submitted[order.ID] = cachedFill{fill: cloneFill(fill), at: now}
	case lookup.FilledQuantity > 0 && lookup.RemainingQuantity > 0:
		next.State = OrderStatePartiallyFilled
		fill = synthesizeProgressFill(order, lookup)
		tracked.fill = cloneFill(fill)
	case lookup.Active:
		next.State = OrderStateWorking
	case lookup.Done:
		next.State = terminalStateFromBrokerStatus(lookup.Status)
	default:
		next.State = prev.State
	}
	next.ExecutionQuality = buildExecutionQuality(order, next, now)

	tracked.snapshot = next
	changed := orderSnapshotChanged(prev, next, tracked.fill, fill)
	if !changed {
		m.mu.Unlock()
		return OrderUpdate{}, false
	}

	update := OrderUpdate{
		Snapshot: next,
		Fill:     fill,
	}
	record := persistedOrderFromTracked(tracked)
	m.mu.Unlock()
	m.persistTrackedOrder(record)
	return update, true
}

func (m *Manager) recordPendingOrder(order model.Order, pending *ibkr.PendingOrderError) {
	if pending == nil || order.ID == "" {
		return
	}
	now := time.Now().UTC()

	m.mu.Lock()
	m.pruneExpiredLocked(now)
	m.tracked[order.ID] = &trackedOrder{
		order: order,
		snapshot: OrderSnapshot{
			OrderID:       order.ID,
			ThesisID:      order.ThesisID,
			DeskID:        order.DeskID,
			DisplaySymbol: order.DisplaySymbol(),
			BrokerOrderID: pending.OrderID,
			State:         OrderStateWorking,
			BrokerStatus:  pending.Status,
			Quantity:      order.Quantity,
			SubmittedAt:   now,
			UpdatedAt:     now,
			LastError:     pending.Error(),
			Paper:         m.ibkr.IsPaper(),
			ExecutionQuality: buildExecutionQuality(order, OrderSnapshot{
				Quantity:          order.Quantity,
				FilledQuantity:    0,
				RemainingQuantity: order.Quantity,
				SubmittedAt:       now,
				UpdatedAt:         now,
			}, now),
		},
	}
	record := persistedOrderFromTracked(m.tracked[order.ID])
	m.mu.Unlock()
	m.persistTrackedOrder(record)
}

func (m *Manager) recordFilledOrder(order model.Order, fill *model.Fill) {
	if order.ID == "" || fill == nil {
		return
	}
	now := time.Now().UTC()
	if !fill.FilledAt.IsZero() {
		now = fill.FilledAt.UTC()
	}

	m.mu.Lock()
	m.pruneExpiredLocked(now)
	m.tracked[order.ID] = &trackedOrder{
		order: order,
		fill:  cloneFill(fill),
		snapshot: OrderSnapshot{
			OrderID:           order.ID,
			ThesisID:          order.ThesisID,
			DeskID:            order.DeskID,
			DisplaySymbol:     fill.DisplaySymbol(),
			BrokerOrderID:     fill.IBKROrderID,
			State:             OrderStateFilled,
			BrokerStatus:      "filled",
			Quantity:          order.Quantity,
			FilledQuantity:    fill.Quantity,
			RemainingQuantity: 0,
			AvgFillPrice:      fill.AvgPrice,
			LastFillPrice:     fill.AvgPrice,
			SubmittedAt:       now,
			UpdatedAt:         now,
			Paper:             m.ibkr.IsPaper(),
		},
	}
	m.tracked[order.ID].snapshot.ExecutionQuality = buildExecutionQuality(order, m.tracked[order.ID].snapshot, now)
	record := persistedOrderFromTracked(m.tracked[order.ID])
	m.mu.Unlock()
	m.persistTrackedOrder(record)
}

func (m *Manager) recordFailedOrder(order model.Order, err error) {
	if order.ID == "" || err == nil {
		return
	}
	now := time.Now().UTC()

	m.mu.Lock()
	m.pruneExpiredLocked(now)
	tracked, ok := m.tracked[order.ID]
	if !ok {
		tracked = &trackedOrder{order: order}
		m.tracked[order.ID] = tracked
	}
	tracked.snapshot = OrderSnapshot{
		OrderID:       order.ID,
		ThesisID:      order.ThesisID,
		DeskID:        order.DeskID,
		DisplaySymbol: order.DisplaySymbol(),
		State:         OrderStateFailed,
		Quantity:      order.Quantity,
		SubmittedAt:   chooseEarlier(tracked.snapshot.SubmittedAt, now),
		UpdatedAt:     now,
		LastError:     err.Error(),
		Paper:         m.ibkr.IsPaper(),
	}
	tracked.snapshot.ExecutionQuality = buildExecutionQuality(order, tracked.snapshot, now)
	record := persistedOrderFromTracked(tracked)
	m.mu.Unlock()
	m.persistTrackedOrder(record)
}

func (m *Manager) lookupWorkingOrderSnapshot(orderID string) (OrderSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked(time.Now().UTC())
	tracked, ok := m.tracked[orderID]
	if !ok || !tracked.snapshot.IsWorking() {
		return OrderSnapshot{}, false
	}
	return tracked.snapshot, true
}

func terminalStateFromBrokerStatus(status string) OrderState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "filled":
		return OrderStateFilled
	case "partiallyfilled":
		return OrderStatePartiallyFilled
	case "cancelled", "apicancelled", "inactive":
		return OrderStateCancelled
	default:
		return OrderStateFailed
	}
}

func buildExecutionQuality(order model.Order, snapshot OrderSnapshot, asOf time.Time) ExecutionQuality {
	quality := ExecutionQuality{}
	if order.ExecutionIntent != nil {
		quality.DecisionPrice = order.ExecutionIntent.DecisionPrice
		quality.ReferencePrice = order.ExecutionIntent.ReferencePrice
	}
	if asOf.IsZero() {
		asOf = snapshot.UpdatedAt
	}
	if !snapshot.SubmittedAt.IsZero() && !asOf.IsZero() {
		age := asOf.Sub(snapshot.SubmittedAt.UTC())
		if age < 0 {
			age = 0
		}
		quality.WorkingAgeSeconds = age.Seconds()
	}
	if snapshot.Quantity > 0 {
		quality.FillRatio = clamp01(snapshot.FilledQuantity / snapshot.Quantity)
	}
	if snapshot.AvgFillPrice > 0 {
		quality.ImplementationShortfall, quality.ImplementationShortfallBps = executionShortfall(
			order.Direction,
			quality.DecisionPrice,
			snapshot.AvgFillPrice,
		)
		quality.ReferenceShortfall, quality.ReferenceShortfallBps = executionShortfall(
			order.Direction,
			quality.ReferencePrice,
			snapshot.AvgFillPrice,
		)
	}
	return quality
}

func executionShortfall(direction model.TradeDirection, baselinePrice, realizedPrice float64) (float64, float64) {
	if baselinePrice <= 0 || realizedPrice <= 0 {
		return 0, 0
	}
	shortfall := realizedPrice - baselinePrice
	if direction == model.Short {
		shortfall = baselinePrice - realizedPrice
	}
	return shortfall, (shortfall / baselinePrice) * 10000
}

func clamp01(v float64) float64 {
	switch {
	case math.IsNaN(v), math.IsInf(v, 0):
		return 0
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func orderSnapshotChanged(before, after OrderSnapshot, beforeFill, afterFill *model.Fill) bool {
	if before.OrderID != after.OrderID ||
		before.ThesisID != after.ThesisID ||
		before.DeskID != after.DeskID ||
		before.DisplaySymbol != after.DisplaySymbol ||
		before.BrokerOrderID != after.BrokerOrderID ||
		before.State != after.State ||
		before.BrokerStatus != after.BrokerStatus ||
		before.Quantity != after.Quantity ||
		before.FilledQuantity != after.FilledQuantity ||
		before.RemainingQuantity != after.RemainingQuantity ||
		before.AvgFillPrice != after.AvgFillPrice ||
		before.LastFillPrice != after.LastFillPrice ||
		!before.SubmittedAt.Equal(after.SubmittedAt) ||
		before.LastError != after.LastError ||
		before.Paper != after.Paper ||
		before.ExecutionQuality != after.ExecutionQuality {
		return true
	}
	return !fillsEqual(beforeFill, afterFill)
}

func fillsEqual(a, b *model.Fill) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	if a.OrderID != b.OrderID ||
		a.IBKROrderID != b.IBKROrderID ||
		a.Structure != b.Structure ||
		a.Instrument != b.Instrument ||
		a.Direction != b.Direction ||
		a.Quantity != b.Quantity ||
		a.AvgPrice != b.AvgPrice ||
		a.Commission != b.Commission ||
		!a.FilledAt.Equal(b.FilledAt) {
		return false
	}
	if len(a.Legs) != len(b.Legs) {
		return false
	}
	for i := range a.Legs {
		if a.Legs[i] != b.Legs[i] {
			return false
		}
	}
	return true
}

func chooseEarlier(current, fallback time.Time) time.Time {
	switch {
	case current.IsZero():
		return fallback
	case fallback.IsZero():
		return current
	case fallback.Before(current):
		return fallback
	default:
		return current
	}
}

func normalizePendingOrderError(snapshot OrderSnapshot) error {
	return &PendingFillError{
		OrderID: snapshot.BrokerOrderID,
		Status:  snapshot.BrokerStatus,
		Cause:   fmt.Errorf("order %s already working at broker", snapshot.OrderID),
	}
}

func isPendingBrokerError(err error) bool {
	var pending *PendingFillError
	return errors.As(err, &pending)
}

func persistedOrderFromTracked(tracked *trackedOrder) PersistedOrder {
	if tracked == nil {
		return PersistedOrder{}
	}
	return PersistedOrder{
		Order:    tracked.order,
		Snapshot: tracked.snapshot,
		Fill:     cloneFill(tracked.fill),
	}
}

func synthesizeProgressFill(order model.Order, lookup ibkr.OrderLookup) *model.Fill {
	if lookup.FilledQuantity <= 0 || lookup.AvgFillPrice <= 0 {
		return nil
	}
	return &model.Fill{
		OrderID:     order.ID,
		IBKROrderID: lookup.OrderID,
		Structure:   order.Structure,
		Instrument:  order.PrimaryInstrument(),
		Legs:        append([]model.TradeLeg(nil), order.Legs...),
		Direction:   order.Direction,
		Quantity:    lookup.FilledQuantity,
		AvgPrice:    lookup.AvgFillPrice,
	}
}

func (m *Manager) persistTrackedOrder(record PersistedOrder) {
	if m == nil || m.journal == nil {
		return
	}
	if record.Snapshot.OrderID == "" && record.Order.ID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.journal.UpsertWorkingOrder(ctx, record); err != nil {
		m.log.Warn("persist working order failed",
			"order_id", firstNonEmptyOrderID(record),
			"state", record.Snapshot.State,
			"error", err,
		)
	}
}

func firstNonEmptyOrderID(record PersistedOrder) string {
	if record.Snapshot.OrderID != "" {
		return record.Snapshot.OrderID
	}
	return record.Order.ID
}
