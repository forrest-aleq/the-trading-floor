package execution

import (
	"context"
	"errors"
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
	cancelled []int64
}

type testOrderJournal struct {
	mu      sync.Mutex
	records map[string]PersistedOrder
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
