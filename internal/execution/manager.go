package execution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
	"golang.org/x/sync/singleflight"
)

type Broker interface {
	IsConnected() bool
	IsPaper() bool
	PlaceOrder(context.Context, model.Order) (*model.Fill, error)
	CancelOrder(context.Context, int64) error
	GetOrderStatus(context.Context, model.Order, int64) (*ibkr.OrderLookup, error)
	GetPositions(context.Context) ([]ibkr.IBKRPosition, error)
	GetAccountSummary(context.Context) (*ibkr.AccountSummary, error)
}

type OrderJournal interface {
	UpsertWorkingOrder(context.Context, PersistedOrder) error
	LoadWorkingOrders(context.Context) ([]PersistedOrder, error)
}

type BrokerOrderFailure struct {
	Order          model.Order
	BrokerOrderID  int64
	Status         string
	Cause          string
	Hint           string
	Error          string
	Unacknowledged bool
	At             time.Time
}

const (
	BrokerFailureCauseUnacknowledged      = "broker_order_unacknowledged"
	BrokerFailureCauseTWSOrderPrecautions = "tws_api_order_precautions"

	BrokerFailureHintTWSOrderPrecautions = "TWS API order precautions are holding API orders; accept the TWS Bypass Order Precautions for API Orders prompt or disable broker entries."
)

type BrokerOrderFailureObserver interface {
	RecordBrokerOrderFailure(BrokerOrderFailure)
}

type CapabilityTokenValidator interface {
	ValidateCapabilityToken(*model.CapToken, model.Order) error
}

type PersistedOrder struct {
	Order    model.Order
	Snapshot OrderSnapshot
	Fill     *model.Fill
}

// Manager handles order lifecycle
type Manager struct {
	ibkr    Broker
	log     *slog.Logger
	journal OrderJournal
	token   CapabilityTokenValidator

	mu               sync.Mutex
	submitted        map[string]cachedFill
	tracked          map[string]*trackedOrder
	failureObserver  BrokerOrderFailureObserver
	group            singleflight.Group
	submittedTTL     time.Duration
	cleanupInterval  time.Duration
	lastCacheCleanup time.Time
}

var submitResultGrace = readManagerDurationEnv("EXECUTION_SUBMIT_RESULT_GRACE", 1500*time.Millisecond)

func NewManager(ibkrClient Broker) *Manager {
	return NewManagerWithJournal(ibkrClient, nil)
}

func NewManagerWithJournal(ibkrClient Broker, journal OrderJournal) *Manager {
	return &Manager{
		ibkr:            ibkrClient,
		log:             slog.Default().With("component", "execution"),
		journal:         journal,
		submitted:       make(map[string]cachedFill),
		tracked:         make(map[string]*trackedOrder),
		submittedTTL:    24 * time.Hour,
		cleanupInterval: 15 * time.Minute,
	}
}

func (m *Manager) SetTokenValidator(validator CapabilityTokenValidator) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = validator
}

func (m *Manager) SetBrokerOrderFailureObserver(observer BrokerOrderFailureObserver) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failureObserver = observer
}

func (m *Manager) HydrateWorkingOrders(ctx context.Context) error {
	if m == nil || m.journal == nil {
		return nil
	}

	records, err := m.journal.LoadWorkingOrders(ctx)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, record := range records {
		orderID := record.Order.ID
		if orderID == "" {
			orderID = record.Snapshot.OrderID
		}
		if orderID == "" {
			continue
		}
		record.Order.ID = orderID
		record.Snapshot.OrderID = orderID
		m.tracked[orderID] = &trackedOrder{
			order:    record.Order,
			snapshot: record.Snapshot,
			fill:     cloneFill(record.Fill),
		}
	}
	m.log.Info("hydrated working orders from journal", "count", len(records))
	return nil
}

type cachedFill struct {
	fill *model.Fill
	at   time.Time
}

type PendingFillError struct {
	OrderID int64
	Status  string
	Cause   error
}

func (e *PendingFillError) Error() string {
	if e == nil {
		return "pending fill"
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	if e.Status != "" {
		return fmt.Sprintf("pending fill with status %s", e.Status)
	}
	return "pending fill"
}

func (e *PendingFillError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Submit places an order through IBKR
func (m *Manager) Submit(ctx context.Context, token *model.CapToken, order model.Order) (*model.Fill, error) {
	return m.submitOnce(ctx, order.ID, func() (*model.Fill, error) {
		if err := m.validateCapabilityToken(token, order); err != nil {
			return nil, err
		}

		// Validate connection
		if !m.ibkr.IsConnected() {
			return nil, fmt.Errorf("IBKR not connected")
		}

		m.log.Info("submitting order",
			"thesis_id", order.ThesisID,
			"desk_id", order.DeskID,
			"symbol", order.DisplaySymbol(),
			"direction", order.Direction,
			"quantity", order.Quantity,
			"type", order.OrderType,
			"structure", order.Structure,
			"paper", m.ibkr.IsPaper(),
		)

		fill, err := m.ibkr.PlaceOrder(ctx, order)
		if err != nil {
			var pending *ibkr.PendingOrderError
			if errors.As(err, &pending) {
				m.recordPendingOrder(order, pending)
				return nil, &PendingFillError{
					OrderID: pending.OrderID,
					Status:  pending.Status,
					Cause:   err,
				}
			}
			m.log.Error("order failed",
				"thesis_id", order.ThesisID,
				"error", err,
			)
			m.notifyBrokerOrderFailure(order, err)
			m.recordFailedOrder(order, err)
			return nil, fmt.Errorf("place order: %w", err)
		}

		m.recordFilledOrder(order, fill)
		m.log.Info("order filled",
			"thesis_id", order.ThesisID,
			"symbol", fill.DisplaySymbol(),
			"price", fill.AvgPrice,
			"quantity", fill.Quantity,
		)

		return fill, nil
	})
}

func (m *Manager) notifyBrokerOrderFailure(order model.Order, err error) {
	if m == nil || err == nil {
		return
	}
	var unack *ibkr.UnacknowledgedOrderError
	if !errors.As(err, &unack) {
		return
	}

	m.mu.Lock()
	observer := m.failureObserver
	m.mu.Unlock()
	if observer == nil {
		return
	}

	cause, hint := classifyBrokerOrderFailure(unack, err)
	observer.RecordBrokerOrderFailure(BrokerOrderFailure{
		Order:          order,
		BrokerOrderID:  unack.OrderID,
		Status:         unack.Status,
		Cause:          cause,
		Hint:           hint,
		Error:          err.Error(),
		Unacknowledged: true,
		At:             time.Now().UTC(),
	})
}

func classifyBrokerOrderFailure(unack *ibkr.UnacknowledgedOrderError, err error) (string, string) {
	if unack == nil {
		return "", ""
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	if strings.EqualFold(unack.Status, "ApiPending") || strings.Contains(errText, "TWS API order precautions") {
		return BrokerFailureCauseTWSOrderPrecautions, BrokerFailureHintTWSOrderPrecautions
	}
	return BrokerFailureCauseUnacknowledged, ""
}

func (m *Manager) validateCapabilityToken(token *model.CapToken, order model.Order) error {
	if token == nil {
		return fmt.Errorf("missing capability token")
	}
	if m == nil {
		return nil
	}
	m.mu.Lock()
	validator := m.token
	m.mu.Unlock()
	if validator == nil {
		return nil
	}
	if err := validator.ValidateCapabilityToken(token, order); err != nil {
		return fmt.Errorf("invalid capability token: %w", err)
	}
	return nil
}

// SubmitExit closes an existing position. Exits intentionally bypass capability tokens:
// risk should never block flattening exposure.
func (m *Manager) SubmitExit(ctx context.Context, order model.Order) (*model.Fill, error) {
	return m.submitOnce(ctx, order.ID, func() (*model.Fill, error) {
		if !m.ibkr.IsConnected() {
			return nil, fmt.Errorf("IBKR not connected")
		}

		m.log.Info("submitting exit order",
			"thesis_id", order.ThesisID,
			"desk_id", order.DeskID,
			"symbol", order.DisplaySymbol(),
			"direction", order.Direction,
			"quantity", order.Quantity,
			"type", order.OrderType,
			"structure", order.Structure,
			"paper", m.ibkr.IsPaper(),
		)

		fill, err := m.ibkr.PlaceOrder(ctx, order)
		if err != nil {
			m.log.Error("exit order failed",
				"thesis_id", order.ThesisID,
				"error", err,
			)
			return nil, fmt.Errorf("place exit order: %w", err)
		}

		m.log.Info("exit order filled",
			"thesis_id", order.ThesisID,
			"symbol", fill.DisplaySymbol(),
			"price", fill.AvgPrice,
			"quantity", fill.Quantity,
		)

		return fill, nil
	})
}

func (m *Manager) submitOnce(ctx context.Context, orderID string, fn func() (*model.Fill, error)) (*model.Fill, error) {
	if orderID == "" {
		return fn()
	}

	if snapshot, ok := m.lookupWorkingOrderSnapshot(orderID); ok {
		m.log.Warn("duplicate working order submission suppressed", "order_id", orderID, "broker_order_id", snapshot.BrokerOrderID)
		return nil, normalizePendingOrderError(snapshot)
	}
	if fill, ok := m.lookupSubmitted(orderID); ok {
		m.log.Warn("duplicate order submission suppressed", "order_id", orderID)
		return fill, nil
	}

	resultCh := m.group.DoChan(orderID, func() (any, error) {
		if fill, ok := m.lookupSubmitted(orderID); ok {
			return fill, nil
		}
		fill, err := fn()
		if err != nil || fill == nil {
			return fill, err
		}
		m.storeSubmitted(orderID, fill)
		return cloneFill(fill), nil
	})

	select {
	case <-ctx.Done():
		if submitResultGrace <= 0 {
			return nil, ctx.Err()
		}
		timer := time.NewTimer(submitResultGrace)
		defer timer.Stop()
		select {
		case result := <-resultCh:
			return consumeSubmitResult(result)
		case <-timer.C:
			return nil, ctx.Err()
		}
	case result := <-resultCh:
		return consumeSubmitResult(result)
	}
}

func consumeSubmitResult(result singleflight.Result) (*model.Fill, error) {
	if result.Err != nil {
		return nil, result.Err
	}
	fill, _ := result.Val.(*model.Fill)
	return cloneFill(fill), nil
}

func readManagerDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func cloneFill(fill *model.Fill) *model.Fill {
	if fill == nil {
		return nil
	}
	cloned := *fill
	cloned.Legs = append([]model.TradeLeg(nil), fill.Legs...)
	return &cloned
}

func (m *Manager) lookupSubmitted(orderID string) (*model.Fill, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked(time.Now().UTC())
	cached, ok := m.submitted[orderID]
	if !ok || cached.fill == nil {
		return nil, false
	}
	return cloneFill(cached.fill), true
}

func (m *Manager) storeSubmitted(orderID string, fill *model.Fill) {
	if fill == nil {
		return
	}

	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneExpiredLocked(now)
	m.submitted[orderID] = cachedFill{
		fill: cloneFill(fill),
		at:   now,
	}
}

func (m *Manager) pruneExpiredLocked(now time.Time) {
	if m.submittedTTL <= 0 {
		return
	}
	if !m.lastCacheCleanup.IsZero() && now.Sub(m.lastCacheCleanup) < m.cleanupInterval {
		return
	}
	for orderID, cached := range m.submitted {
		if cached.at.IsZero() || now.Sub(cached.at) <= m.submittedTTL {
			continue
		}
		delete(m.submitted, orderID)
	}
	for orderID, tracked := range m.tracked {
		if tracked == nil || tracked.snapshot.IsWorking() {
			continue
		}
		if tracked.snapshot.UpdatedAt.IsZero() || now.Sub(tracked.snapshot.UpdatedAt) <= m.submittedTTL {
			continue
		}
		delete(m.tracked, orderID)
	}
	m.lastCacheCleanup = now
}

// Cancel cancels a pending order
func (m *Manager) Cancel(ctx context.Context, orderID int64) error {
	return m.ibkr.CancelOrder(ctx, orderID)
}

func (m *Manager) IsPaper() bool {
	if m == nil || m.ibkr == nil {
		return false
	}
	return m.ibkr.IsPaper()
}

// GetPositions returns current IBKR positions for reconciliation
func (m *Manager) GetPositions(ctx context.Context) ([]ibkr.IBKRPosition, error) {
	return m.ibkr.GetPositions(ctx)
}

// GetAccountSummary returns account balance
func (m *Manager) GetAccountSummary(ctx context.Context) (*ibkr.AccountSummary, error) {
	return m.ibkr.GetAccountSummary(ctx)
}
