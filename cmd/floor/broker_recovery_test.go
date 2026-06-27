package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/pkg/model"
)

func openBrokerRecoveryTestPosition(t *testing.T, bk *book.Book) *model.Position {
	t.Helper()
	inst := model.Instrument{
		Symbol:   "BB",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
		ConID:    131217639,
	}
	pos := bk.OpenPosition(&model.Fill{
		OrderID:    "broker-recovered:conid:131217639",
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   10,
		AvgPrice:   3.00,
	}, &model.Thesis{
		ID:         "broker-recovery-thesis",
		DeskID:     brokerRecoveryDeskID,
		Instrument: inst,
		Direction:  model.Long,
	})
	if pos == nil {
		t.Fatal("expected broker recovery position")
	}
	return pos
}

func TestHandleBrokerRecoveryWorkingOrderUpdateClosesRecoveredPosition(t *testing.T) {
	bk := book.NewBook(&fakeBroker{}, 100000)
	pos := openBrokerRecoveryTestPosition(t, bk)

	handled := handleBrokerRecoveryWorkingOrderUpdate(context.Background(), bk, nil, execution.OrderUpdate{
		Snapshot: execution.OrderSnapshot{
			OrderID:       pos.ID + "-close",
			DeskID:        brokerRecoveryDeskID,
			State:         execution.OrderStateFilled,
			BrokerOrderID: 1563,
			AvgFillPrice:  9.99,
		},
		Fill: &model.Fill{AvgPrice: 4.25},
	})

	if !handled {
		t.Fatal("expected broker recovery update to be handled")
	}
	closed, ok := bk.GetPosition(pos.ID)
	if !ok || closed.Status != "closed" {
		t.Fatalf("expected recovered position to be closed, got ok=%v pos=%+v", ok, closed)
	}
	if closed.RealizedPnL != 12.5 {
		t.Fatalf("realized pnl = %.2f, want 12.50", closed.RealizedPnL)
	}
	if len(bk.GetOpenPositions()) != 0 {
		t.Fatalf("expected no open positions, got %+v", bk.GetOpenPositions())
	}
}

func TestHandleBrokerRecoveryWorkingOrderUpdateKeepsWorkingPositionOpen(t *testing.T) {
	bk := book.NewBook(&fakeBroker{}, 100000)
	pos := openBrokerRecoveryTestPosition(t, bk)

	handled := handleBrokerRecoveryWorkingOrderUpdate(context.Background(), bk, nil, execution.OrderUpdate{
		Snapshot: execution.OrderSnapshot{
			OrderID:       pos.ID + "-close",
			DeskID:        brokerRecoveryDeskID,
			State:         execution.OrderStateWorking,
			BrokerStatus:  "PreSubmitted",
			BrokerOrderID: 1563,
		},
	})

	if !handled {
		t.Fatal("expected working broker recovery update to be handled")
	}
	stillOpen, ok := bk.GetPosition(pos.ID)
	if !ok || stillOpen.Status != "open" {
		t.Fatalf("expected recovered position to remain open, got ok=%v pos=%+v", ok, stillOpen)
	}
}

func TestHandleBrokerRecoveryWorkingOrderUpdateLeavesPartialFillOpen(t *testing.T) {
	bk := book.NewBook(&fakeBroker{}, 100000)
	pos := openBrokerRecoveryTestPosition(t, bk)

	handled := handleBrokerRecoveryWorkingOrderUpdate(context.Background(), bk, nil, execution.OrderUpdate{
		Snapshot: execution.OrderSnapshot{
			OrderID:           pos.ID + "-close",
			DeskID:            brokerRecoveryDeskID,
			State:             execution.OrderStatePartiallyFilled,
			BrokerStatus:      "Submitted",
			BrokerOrderID:     1563,
			FilledQuantity:    4,
			RemainingQuantity: 6,
			AvgFillPrice:      4.25,
		},
	})

	if !handled {
		t.Fatal("expected partial broker recovery update to be handled")
	}
	stillOpen, ok := bk.GetPosition(pos.ID)
	if !ok || stillOpen.Status != "open" || stillOpen.Quantity != 10 {
		t.Fatalf("expected partial fill to leave full recovered position open for reconciliation, got ok=%v pos=%+v", ok, stillOpen)
	}
}

func TestHandleBrokerRecoveryWorkingOrderUpdateLeavesTerminalPartialFillOpen(t *testing.T) {
	bk := book.NewBook(&fakeBroker{}, 100000)
	pos := openBrokerRecoveryTestPosition(t, bk)

	handled := handleBrokerRecoveryWorkingOrderUpdate(context.Background(), bk, nil, execution.OrderUpdate{
		Snapshot: execution.OrderSnapshot{
			OrderID:           pos.ID + "-close",
			DeskID:            brokerRecoveryDeskID,
			State:             execution.OrderStateCancelled,
			BrokerStatus:      "Cancelled",
			BrokerOrderID:     1563,
			FilledQuantity:    4,
			RemainingQuantity: 6,
			AvgFillPrice:      4.25,
		},
	})

	if !handled {
		t.Fatal("expected terminal partial broker recovery update to be handled")
	}
	stillOpen, ok := bk.GetPosition(pos.ID)
	if !ok || stillOpen.Status != "open" || stillOpen.Quantity != 10 {
		t.Fatalf("expected terminal partial fill to leave position open for reconciliation, got ok=%v pos=%+v", ok, stillOpen)
	}
}

func TestHandleBrokerRecoveryWorkingOrderUpdateIgnoresOtherDesks(t *testing.T) {
	handled := handleBrokerRecoveryWorkingOrderUpdate(context.Background(), nil, nil, execution.OrderUpdate{
		Snapshot: execution.OrderSnapshot{
			OrderID: "desk-order-close",
			DeskID:  "sector-tech-a",
			State:   execution.OrderStateFilled,
		},
	})
	if handled {
		t.Fatal("expected non-recovery desk update to use normal desk flow")
	}
}

func TestBrokerRecoveryPositionIDFromCloseOrder(t *testing.T) {
	positionID, ok := brokerRecoveryPositionIDFromCloseOrder("broker-recovered:conid:131217639-close")
	if !ok || positionID != "broker-recovered:conid:131217639" {
		t.Fatalf("unexpected position id parse: id=%q ok=%v", positionID, ok)
	}
	if _, ok := brokerRecoveryPositionIDFromCloseOrder("broker-recovered:conid:131217639"); ok {
		t.Fatal("expected non-close order id to fail")
	}
}

func TestBrokerRecoveryExitPricePrefersFillThenSnapshot(t *testing.T) {
	if got := brokerRecoveryExitPrice(execution.OrderUpdate{
		Snapshot: execution.OrderSnapshot{AvgFillPrice: 9.99},
		Fill:     &model.Fill{AvgPrice: 4.25},
	}); got != 4.25 {
		t.Fatalf("exit price with fill = %.2f, want 4.25", got)
	}
	if got := brokerRecoveryExitPrice(execution.OrderUpdate{
		Snapshot: execution.OrderSnapshot{AvgFillPrice: 5.50},
	}); got != 5.50 {
		t.Fatalf("exit price from avg snapshot = %.2f, want 5.50", got)
	}
	if got := brokerRecoveryExitPrice(execution.OrderUpdate{
		Snapshot: execution.OrderSnapshot{LastFillPrice: 6.75},
	}); got != 6.75 {
		t.Fatalf("exit price from last snapshot = %.2f, want 6.75", got)
	}
}

func TestPendingFillErrorMatchesWrappedError(t *testing.T) {
	pending := &execution.PendingFillError{OrderID: 1563, Status: "PreSubmitted"}
	got, ok := pendingFillError(fmt.Errorf("submit exit: %w", pending))
	if !ok || got != pending {
		t.Fatalf("expected wrapped pending fill error, got ok=%v err=%+v", ok, got)
	}
}
