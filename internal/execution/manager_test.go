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
func (b *testBroker) CancelOrder(_ context.Context, _ int64) error { return nil }
func (b *testBroker) GetPositions(_ context.Context) ([]ibkr.IBKRPosition, error) {
	return nil, nil
}
func (b *testBroker) GetAccountSummary(_ context.Context) (*ibkr.AccountSummary, error) {
	return &ibkr.AccountSummary{}, nil
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
