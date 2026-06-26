package execution

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

type testBroker struct {
	connected atomic.Bool
	calls     atomic.Int64
	delay     time.Duration
	err       error
	mu        sync.Mutex
	lookups   map[int64]*ibkr.OrderLookup
	lookupErr error
	cancelled []int64
}

type testOrderJournal struct {
	mu      sync.Mutex
	records map[string]PersistedOrder
}

type testTokenValidator struct {
	err   error
	calls atomic.Int64
}

type testBrokerFailureObserver struct {
	mu       sync.Mutex
	failures []BrokerOrderFailure
}

func (o *testBrokerFailureObserver) RecordBrokerOrderFailure(failure BrokerOrderFailure) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures = append(o.failures, failure)
}

func (v *testTokenValidator) ValidateCapabilityToken(*model.CapToken, model.Order) error {
	v.calls.Add(1)
	return v.err
}

func (j *testOrderJournal) UpsertWorkingOrder(_ context.Context, record PersistedOrder) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.records == nil {
		j.records = make(map[string]PersistedOrder)
	}
	orderID := record.Order.ID
	if orderID == "" {
		orderID = record.Snapshot.OrderID
	}
	record.Order.ID = orderID
	record.Snapshot.OrderID = orderID
	if record.Fill != nil {
		record.Fill = cloneFill(record.Fill)
	}
	j.records[orderID] = record
	return nil
}

func (j *testOrderJournal) LoadWorkingOrders(_ context.Context) ([]PersistedOrder, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	records := make([]PersistedOrder, 0, len(j.records))
	for _, record := range j.records {
		cp := record
		cp.Fill = cloneFill(record.Fill)
		records = append(records, cp)
	}
	return records, nil
}

func (b *testBroker) IsConnected() bool { return b.connected.Load() }
func (b *testBroker) IsPaper() bool     { return true }
func (b *testBroker) PlaceOrder(_ context.Context, order model.Order) (*model.Fill, error) {
	b.calls.Add(1)
	if b.err != nil {
		return nil, b.err
	}
	if b.delay > 0 {
		time.Sleep(b.delay)
	}
	return &model.Fill{
		OrderID:    order.ID,
		Instrument: order.PrimaryInstrument(),
		Direction:  order.Direction,
		Quantity:   order.Quantity,
		AvgPrice:   order.LimitPrice,
		FilledAt:   time.Now().UTC(),
	}, nil
}
func (b *testBroker) CancelOrder(_ context.Context, orderID int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancelled = append(b.cancelled, orderID)
	return nil
}
func (b *testBroker) GetPositions(_ context.Context) ([]ibkr.IBKRPosition, error) {
	return nil, nil
}
func (b *testBroker) GetAccountSummary(_ context.Context) (*ibkr.AccountSummary, error) {
	return &ibkr.AccountSummary{}, nil
}
func (b *testBroker) GetOrderStatus(_ context.Context, _ model.Order, orderID int64) (*ibkr.OrderLookup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lookupErr != nil {
		return nil, b.lookupErr
	}
	if lookup, ok := b.lookups[orderID]; ok {
		cp := *lookup
		return &cp, nil
	}
	return nil, nil
}

func TestSubmitSuppressesDuplicateOrderIDs(t *testing.T) {
	broker := &testBroker{}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-123",
		ThesisID:   "thesis-123",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   10,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}
	token := &model.CapToken{}

	first, err := manager.Submit(context.Background(), token, order)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	second, err := manager.Submit(context.Background(), token, order)
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}
	if broker.calls.Load() != 1 {
		t.Fatalf("expected exactly one broker call, got %d", broker.calls.Load())
	}
	if first == nil || second == nil || first.OrderID != second.OrderID {
		t.Fatalf("expected duplicate submit to return cached fill, got first=%+v second=%+v", first, second)
	}
}

func TestSubmitRejectsInvalidCapabilityTokenBeforeBrokerCall(t *testing.T) {
	broker := &testBroker{}
	broker.connected.Store(true)
	manager := NewManager(broker)
	validator := &testTokenValidator{err: errors.New("signature mismatch")}
	manager.SetTokenValidator(validator)

	order := model.Order{
		ID:         "order-invalid-token",
		ThesisID:   "thesis-invalid-token",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   10,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	_, err := manager.Submit(context.Background(), &model.CapToken{}, order)
	if err == nil {
		t.Fatal("expected invalid capability token error")
	}
	if !strings.Contains(err.Error(), "invalid capability token") {
		t.Fatalf("expected invalid token error, got %v", err)
	}
	if validator.calls.Load() != 1 {
		t.Fatalf("expected validator to be called once, got %d", validator.calls.Load())
	}
	if broker.calls.Load() != 0 {
		t.Fatalf("expected broker not to be called, got %d", broker.calls.Load())
	}
}

func TestSubmitNotifiesObserverOnUnacknowledgedBrokerOrder(t *testing.T) {
	broker := &testBroker{
		err: &ibkr.UnacknowledgedOrderError{
			OrderID: 777,
			Status:  "ApiPending",
			Cause:   errors.New("ack timeout"),
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)
	observer := &testBrokerFailureObserver{}
	manager.SetBrokerOrderFailureObserver(observer)

	order := model.Order{
		ID:         "order-unack",
		ThesisID:   "thesis-unack",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   10,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	_, err := manager.Submit(context.Background(), &model.CapToken{}, order)
	if err == nil {
		t.Fatal("expected submit to fail")
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.failures) != 1 {
		t.Fatalf("expected one broker failure notification, got %d", len(observer.failures))
	}
	failure := observer.failures[0]
	if !failure.Unacknowledged {
		t.Fatal("expected unacknowledged failure")
	}
	if failure.BrokerOrderID != 777 || failure.Status != "ApiPending" {
		t.Fatalf("unexpected failure notification: %+v", failure)
	}
	if failure.Cause != BrokerFailureCauseTWSOrderPrecautions {
		t.Fatalf("expected TWS order precautions cause, got %q", failure.Cause)
	}
	if failure.Hint == "" {
		t.Fatal("expected remediation hint for TWS order precautions")
	}
	if failure.Order.ID != order.ID || failure.Order.DeskID != order.DeskID {
		t.Fatalf("expected order context in failure notification, got %+v", failure.Order)
	}
}

func TestSubmitDeduplicatesConcurrentInFlightOrders(t *testing.T) {
	broker := &testBroker{delay: 80 * time.Millisecond}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-456",
		ThesisID:   "thesis-456",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "MSFT", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   5,
		OrderType:  model.OrderLimit,
		LimitPrice: 250,
	}
	token := &model.CapToken{}

	var wg sync.WaitGroup
	results := make([]*model.Fill, 3)
	errors := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = manager.Submit(context.Background(), token, order)
		}(i)
	}
	wg.Wait()

	if broker.calls.Load() != 1 {
		t.Fatalf("expected one broker call for concurrent duplicate submits, got %d", broker.calls.Load())
	}
	for i, err := range errors {
		if err != nil {
			t.Fatalf("submit %d failed: %v", i, err)
		}
		if results[i] == nil || results[i].OrderID != order.ID {
			t.Fatalf("submit %d returned bad fill: %+v", i, results[i])
		}
	}
}

func TestSubmittedCacheExpires(t *testing.T) {
	broker := &testBroker{}
	broker.connected.Store(true)
	manager := NewManager(broker)
	manager.submittedTTL = 20 * time.Millisecond
	manager.cleanupInterval = 0

	order := model.Order{
		ID:         "order-expire",
		ThesisID:   "thesis-expire",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "NVDA", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 120,
	}
	token := &model.CapToken{}

	if _, err := manager.Submit(context.Background(), token, order); err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	time.Sleep(35 * time.Millisecond)
	if _, err := manager.Submit(context.Background(), token, order); err != nil {
		t.Fatalf("second submit failed: %v", err)
	}
	if broker.calls.Load() != 2 {
		t.Fatalf("expected cache expiry to force a second broker call, got %d", broker.calls.Load())
	}
}

func TestSubmitReturnsPendingFillErrorForAcceptedUnfilledPaperOrder(t *testing.T) {
	broker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 42,
			Status:  "Submitted",
			Reason:  "order accepted but not filled before execution timeout",
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-pending",
		ThesisID:   "thesis-pending",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	_, err := manager.Submit(context.Background(), &model.CapToken{}, order)
	if err == nil {
		t.Fatal("expected pending fill error")
	}
	var pending *PendingFillError
	if !errors.As(err, &pending) {
		t.Fatalf("expected PendingFillError, got %T", err)
	}
	if pending.OrderID != 42 {
		t.Fatalf("expected order id 42, got %d", pending.OrderID)
	}
	if pending.Status != "Submitted" {
		t.Fatalf("expected submitted status, got %q", pending.Status)
	}
}

func TestHydrateWorkingOrdersSuppressesDuplicateSubmitAfterRestart(t *testing.T) {
	journal := &testOrderJournal{}
	initialBroker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 77,
			Status:  "Submitted",
			Reason:  "accepted but still working",
		},
	}
	initialBroker.connected.Store(true)
	manager := NewManagerWithJournal(initialBroker, journal)

	order := model.Order{
		ID:         "order-restart",
		ThesisID:   "thesis-restart",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "QQQ", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected initial pending fill error")
	}

	recoveredBroker := &testBroker{}
	recoveredBroker.connected.Store(true)
	recovered := NewManagerWithJournal(recoveredBroker, journal)
	if err := recovered.HydrateWorkingOrders(context.Background()); err != nil {
		t.Fatalf("hydrate working orders failed: %v", err)
	}

	if snapshot, ok := recovered.OrderStatus(order.ID); !ok || snapshot.State != OrderStateWorking {
		t.Fatalf("expected hydrated working order snapshot, got ok=%v snapshot=%+v", ok, snapshot)
	}

	if _, err := recovered.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected duplicate submit to be suppressed after hydration")
	}
	if recoveredBroker.calls.Load() != 0 {
		t.Fatalf("expected recovered manager to suppress duplicate without broker call, got %d", recoveredBroker.calls.Load())
	}
}

func TestSubmitPrefersBrokerPendingResultOverContextDeadline(t *testing.T) {
	oldGrace := submitResultGrace
	submitResultGrace = 100 * time.Millisecond
	defer func() { submitResultGrace = oldGrace }()

	broker := &testBroker{
		delay: 35 * time.Millisecond,
		err: &ibkr.PendingOrderError{
			OrderID: 99,
			Status:  "Submitted",
			Reason:  "order accepted but not filled before execution timeout",
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-pending-timeout",
		ThesisID:   "thesis-pending-timeout",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := manager.Submit(ctx, &model.CapToken{}, order)
	if err == nil {
		t.Fatal("expected pending fill error")
	}
	var pending *PendingFillError
	if !errors.As(err, &pending) {
		t.Fatalf("expected PendingFillError, got %T (%v)", err, err)
	}
	if pending.OrderID != 99 {
		t.Fatalf("expected order id 99, got %d", pending.OrderID)
	}
}

func TestSubmitSuppressesDuplicateWhileBrokerOrderWorking(t *testing.T) {
	broker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 77,
			Status:  "Submitted",
			Reason:  "accepted but still working",
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-working",
		ThesisID:   "thesis-working",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "QQQ", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected initial pending fill error")
	}
	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected duplicate submit to be suppressed as pending")
	}
	if broker.calls.Load() != 1 {
		t.Fatalf("expected one broker call while order is still working, got %d", broker.calls.Load())
	}
}

func TestRefreshWorkingOrdersPromotesFill(t *testing.T) {
	broker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 88,
			Status:  "Submitted",
			Reason:  "accepted but still working",
		},
		lookups: map[int64]*ibkr.OrderLookup{
			88: {
				OrderID:           88,
				Status:            "Filled",
				FilledQuantity:    2,
				RemainingQuantity: 0,
				AvgFillPrice:      101.25,
				LastFillPrice:     101.25,
				UpdatedAt:         time.Now().UTC(),
				Done:              true,
				Fill: &model.Fill{
					OrderID:     "order-refresh",
					IBKROrderID: 88,
					Instrument:  model.Instrument{Symbol: "QQQ", SecType: "STK", Currency: "USD", Exchange: "SMART"},
					Direction:   model.Long,
					Quantity:    2,
					AvgPrice:    101.25,
					FilledAt:    time.Now().UTC(),
				},
			},
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-refresh",
		ThesisID:   "thesis-refresh",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "QQQ", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   2,
		OrderType:  model.OrderLimit,
		LimitPrice: 101,
		ExecutionIntent: &model.ExecutionIntent{
			DecisionPrice:  101,
			ReferencePrice: 100.9,
			DecidedAt:      time.Now().UTC(),
		},
	}

	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected initial pending fill error")
	}

	updates := manager.RefreshWorkingOrders(context.Background())
	if len(updates) != 1 {
		t.Fatalf("expected one working order update, got %d", len(updates))
	}
	if updates[0].Snapshot.State != OrderStateFilled {
		t.Fatalf("expected filled state, got %s", updates[0].Snapshot.State)
	}
	if updates[0].Fill == nil || updates[0].Fill.AvgPrice != 101.25 {
		t.Fatalf("expected reconciled fill, got %+v", updates[0].Fill)
	}
	if snapshot, ok := manager.OrderStatus(order.ID); !ok || snapshot.State != OrderStateFilled {
		t.Fatalf("expected tracked order to move to filled, got ok=%v snapshot=%+v", ok, snapshot)
	}
	if snapshot, ok := manager.OrderStatus(order.ID); !ok || snapshot.ExecutionQuality.ImplementationShortfallBps <= 0 {
		t.Fatalf("expected positive shortfall metrics, got ok=%v snapshot=%+v", ok, snapshot)
	}
}

func TestCancelWorkingOrderMarksCancelRequested(t *testing.T) {
	broker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 91,
			Status:  "Submitted",
			Reason:  "accepted but still working",
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-cancel",
		ThesisID:   "thesis-cancel",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected initial pending fill error")
	}
	if err := manager.CancelWorkingOrder(context.Background(), order.ID); err != nil {
		t.Fatalf("cancel working order failed: %v", err)
	}
	if len(broker.cancelled) != 1 || broker.cancelled[0] != 91 {
		t.Fatalf("expected broker cancel for order 91, got %+v", broker.cancelled)
	}
	snapshot, ok := manager.OrderStatus(order.ID)
	if !ok {
		t.Fatal("expected tracked order snapshot after cancel request")
	}
	if snapshot.BrokerStatus != "cancel_requested" {
		t.Fatalf("expected cancel_requested broker status, got %q", snapshot.BrokerStatus)
	}
}

func TestRefreshWorkingOrdersMarksBrokerNotFoundFailed(t *testing.T) {
	broker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 92,
			Status:  "Submitted",
			Reason:  "accepted but still working",
		},
		lookupErr: ibkr.ErrOrderNotFound,
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-not-found",
		ThesisID:   "thesis-not-found",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected initial pending fill error")
	}

	updates := manager.RefreshWorkingOrders(context.Background())
	if len(updates) != 1 {
		t.Fatalf("expected one failed-order update, got %d", len(updates))
	}
	if updates[0].Snapshot.State != OrderStateFailed {
		t.Fatalf("expected failed state, got %s", updates[0].Snapshot.State)
	}
	if updates[0].Snapshot.BrokerStatus != "not_found" {
		t.Fatalf("expected not_found broker status, got %q", updates[0].Snapshot.BrokerStatus)
	}
	if snapshot, ok := manager.OrderStatus(order.ID); !ok || snapshot.State != OrderStateFailed {
		t.Fatalf("expected tracked order to move to failed, got ok=%v snapshot=%+v", ok, snapshot)
	}
}

func TestRefreshWorkingOrdersFailsStaleUnacknowledgedBrokerStatus(t *testing.T) {
	t.Setenv("IBKR_UNACKNOWLEDGED_ORDER_TTL", "1s")

	broker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 94,
			Status:  "ApiPending",
			Reason:  "accepted locally but not yet acknowledged",
		},
		lookups: map[int64]*ibkr.OrderLookup{
			94: {
				OrderID:           94,
				Status:            "ApiPending",
				RemainingQuantity: 1,
				UpdatedAt:         time.Now().UTC().Add(2 * time.Minute),
				Active:            true,
			},
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-stale-apipending",
		ThesisID:   "thesis-stale-apipending",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected initial pending fill error")
	}

	updates := manager.RefreshWorkingOrders(context.Background())
	if len(updates) != 1 {
		t.Fatalf("expected one failed-order update, got %d", len(updates))
	}
	if updates[0].Snapshot.State != OrderStateFailed {
		t.Fatalf("expected stale ApiPending order to fail, got %s", updates[0].Snapshot.State)
	}
	if !strings.Contains(updates[0].Snapshot.LastError, "not acknowledged") {
		t.Fatalf("expected acknowledgement error, got %q", updates[0].Snapshot.LastError)
	}
}

func TestRefreshWorkingOrdersKeepsFreshUnacknowledgedBrokerStatusWorking(t *testing.T) {
	t.Setenv("IBKR_UNACKNOWLEDGED_ORDER_TTL", "1h")

	broker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 95,
			Status:  "ApiPending",
			Reason:  "accepted locally but not yet acknowledged",
		},
		lookups: map[int64]*ibkr.OrderLookup{
			95: {
				OrderID:           95,
				Status:            "ApiPending",
				RemainingQuantity: 1,
				UpdatedAt:         time.Now().UTC(),
				Active:            true,
			},
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-fresh-apipending",
		ThesisID:   "thesis-fresh-apipending",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   1,
		OrderType:  model.OrderLimit,
		LimitPrice: 100,
	}

	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected initial pending fill error")
	}

	updates := manager.RefreshWorkingOrders(context.Background())
	if len(updates) != 1 {
		t.Fatalf("expected one working-order update, got %d", len(updates))
	}
	if updates[0].Snapshot.State != OrderStateWorking {
		t.Fatalf("expected fresh ApiPending order to remain working, got %s", updates[0].Snapshot.State)
	}
}

func TestRefreshWorkingOrdersTracksPartialFillProgress(t *testing.T) {
	broker := &testBroker{
		err: &ibkr.PendingOrderError{
			OrderID: 93,
			Status:  "Submitted",
			Reason:  "accepted but still working",
		},
		lookups: map[int64]*ibkr.OrderLookup{
			93: {
				OrderID:           93,
				Status:            "Submitted",
				FilledQuantity:    1,
				RemainingQuantity: 1,
				AvgFillPrice:      101.5,
				LastFillPrice:     101.5,
				UpdatedAt:         time.Now().UTC(),
				Active:            true,
			},
		},
	}
	broker.connected.Store(true)
	manager := NewManager(broker)

	order := model.Order{
		ID:         "order-partial",
		ThesisID:   "thesis-partial",
		DeskID:     "desk-a",
		Instrument: model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:  model.Long,
		Quantity:   2,
		OrderType:  model.OrderLimit,
		LimitPrice: 101,
		ExecutionIntent: &model.ExecutionIntent{
			DecisionPrice:  101,
			ReferencePrice: 100.8,
			DecidedAt:      time.Now().UTC(),
		},
	}

	if _, err := manager.Submit(context.Background(), &model.CapToken{}, order); err == nil {
		t.Fatal("expected initial pending fill error")
	}

	updates := manager.RefreshWorkingOrders(context.Background())
	if len(updates) != 1 {
		t.Fatalf("expected one working order update, got %d", len(updates))
	}
	if updates[0].Snapshot.State != OrderStatePartiallyFilled {
		t.Fatalf("expected partially_filled state, got %s", updates[0].Snapshot.State)
	}
	if updates[0].Snapshot.ExecutionQuality.FillRatio != 0.5 {
		t.Fatalf("expected fill ratio 0.5, got %.2f", updates[0].Snapshot.ExecutionQuality.FillRatio)
	}
	if updates[0].Snapshot.ExecutionQuality.ImplementationShortfallBps <= 0 {
		t.Fatalf("expected positive shortfall bps, got %.2f", updates[0].Snapshot.ExecutionQuality.ImplementationShortfallBps)
	}
	if updates[0].Fill == nil || updates[0].Fill.Quantity != 1 {
		t.Fatalf("expected cumulative partial fill payload, got %+v", updates[0].Fill)
	}
	if snapshot, ok := manager.OrderStatus(order.ID); !ok || !snapshot.IsWorking() {
		t.Fatalf("expected partially filled order to remain working, got ok=%v snapshot=%+v", ok, snapshot)
	}
}
