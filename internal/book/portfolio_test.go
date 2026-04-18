package book

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

type stubPositionSource struct{}

func (stubPositionSource) GetPositions(ctx context.Context) ([]ibkr.IBKRPosition, error) {
	return nil, nil
}

type stubRuntimeSource struct {
	positions []ibkr.IBKRPosition
	summary   *ibkr.AccountSummary
}

func (s stubRuntimeSource) GetPositions(ctx context.Context) ([]ibkr.IBKRPosition, error) {
	return s.positions, nil
}

func (s stubRuntimeSource) GetAccountSummary(ctx context.Context) (*ibkr.AccountSummary, error) {
	return s.summary, nil
}

type mutableRuntimeSource struct {
	positions    []ibkr.IBKRPosition
	summary      *ibkr.AccountSummary
	positionsErr error
	summaryErr   error
}

func (s *mutableRuntimeSource) GetPositions(ctx context.Context) ([]ibkr.IBKRPosition, error) {
	return s.positions, s.positionsErr
}

func (s *mutableRuntimeSource) GetAccountSummary(ctx context.Context) (*ibkr.AccountSummary, error) {
	return s.summary, s.summaryErr
}

func TestBookMarkKeepsEquityAndUpdatesPnL(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 1000)
	bk.SetDeskCapital("desk-1", 1000)

	inst := model.Instrument{
		Symbol:   "AAPL",
		SecType:  "STK",
		Currency: "USD",
	}

	bk.OpenPosition(&model.Fill{
		OrderID:    "order-1",
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   10,
		AvgPrice:   10,
		FilledAt:   time.Now(),
	}, &model.Thesis{
		ID:         "thesis-1",
		DeskID:     "desk-1",
		Instrument: inst,
		Direction:  model.Long,
	})

	snapshot := bk.Snapshot()
	if snapshot.NAV != 1000 {
		t.Fatalf("expected unchanged NAV after opening long, got %.2f", snapshot.NAV)
	}

	bk.Mark(map[string]float64{"AAPL": 12})
	snapshot = bk.Snapshot()

	if snapshot.NAV != 1020 {
		t.Fatalf("expected NAV 1020 after mark, got %.2f", snapshot.NAV)
	}
	if snapshot.GrossExposure != 120 {
		t.Fatalf("expected gross exposure 120, got %.2f", snapshot.GrossExposure)
	}
	if snapshot.NetExposure != 120 {
		t.Fatalf("expected net exposure 120, got %.2f", snapshot.NetExposure)
	}
}

func TestBookMarksMultiLegVerticalSpread(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 1000)
	bk.SetDeskCapital("desk-1", 1000)

	longCall := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   "20260619",
		Strike:   120,
		Right:    "C",
	}
	shortCall := longCall
	shortCall.Strike = 130

	fill := &model.Fill{
		OrderID:    "spread-1",
		Structure:  "bull_call_spread",
		Instrument: longCall,
		Legs: []model.TradeLeg{
			{Instrument: longCall, Direction: model.Long, Ratio: 1, Quantity: 1, EntryPrice: 4.0},
			{Instrument: shortCall, Direction: model.Short, Ratio: 1, Quantity: 1, EntryPrice: 1.5},
		},
		Direction: model.Long,
		Quantity:  1,
		AvgPrice:  2.5,
		FilledAt:  time.Now(),
	}
	thesis := &model.Thesis{
		ID:         "thesis-2",
		DeskID:     "desk-1",
		Structure:  "bull_call_spread",
		Instrument: longCall,
		Legs: []model.TradeLeg{
			{Instrument: longCall, Direction: model.Long, Ratio: 1, EntryPrice: 4.0},
			{Instrument: shortCall, Direction: model.Short, Ratio: 1, EntryPrice: 1.5},
		},
		Direction: model.Long,
	}

	bk.OpenPosition(fill, thesis)
	bk.Mark(map[string]float64{
		longCall.Key():  5.5,
		shortCall.Key(): 2.0,
	})

	open := bk.GetOpenPositions()
	if len(open) != 1 {
		t.Fatalf("expected one open position, got %d", len(open))
	}
	if got := open[0].CurrentPrice; got != 3.5 {
		t.Fatalf("expected net combo price 3.5, got %.2f", got)
	}
	if got := open[0].UnrealizedPnL; got != 100 {
		t.Fatalf("expected unrealized pnl 100, got %.2f", got)
	}

	snapshot := bk.Snapshot()
	if snapshot.NAV != 1100 {
		t.Fatalf("expected NAV 1100 after combo mark, got %.2f", snapshot.NAV)
	}
	if snapshot.GrossExposure != 750 {
		t.Fatalf("expected gross exposure 750, got %.2f", snapshot.GrossExposure)
	}
}

func TestOpenShadowPositionUsesPositiveFallbackPrice(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 1000)
	thesis := &model.Thesis{
		ID:           "shadow-1",
		DeskID:       "desk-1",
		Instrument:   model.Instrument{Symbol: "TLT", SecType: "STK", Currency: "USD", Exchange: "SMART"},
		Direction:    model.Long,
		PositionSize: 0.01,
		MarketContext: &model.MarketContext{
			CurrentPrice: 91.25,
		},
	}

	pos := bk.OpenShadowPosition(thesis)
	if pos.EntryPrice != 91.25 {
		t.Fatalf("expected shadow entry price from market context, got %.2f", pos.EntryPrice)
	}
	if pos.CurrentPrice != 91.25 {
		t.Fatalf("expected shadow current price from market context, got %.2f", pos.CurrentPrice)
	}

	thesis.EntryPrice = 0
	thesis.MarketContext = nil
	thesis.TargetPrice = 0
	thesis.StopLoss = 0
	thesis.ID = "shadow-2"
	pos = bk.OpenShadowPosition(thesis)
	if pos.EntryPrice <= 0 {
		t.Fatalf("expected positive minimum fallback entry price, got %.4f", pos.EntryPrice)
	}
}

func TestSnapshotPrefersBrokerAccountSummary(t *testing.T) {
	bk := NewBook(stubRuntimeSource{
		summary: &ibkr.AccountSummary{
			NetLiquidation: 1001986,
			Cash:           1001986,
			UnrealizedPnL:  25,
			RealizedPnL:    -10,
		},
	}, 1000)

	bk.reconcile(context.Background())
	snapshot := bk.Snapshot()

	if snapshot.NAV != 1001986 {
		t.Fatalf("expected broker nav 1001986, got %.2f", snapshot.NAV)
	}
	if snapshot.Cash != 1001986 {
		t.Fatalf("expected broker cash 1001986, got %.2f", snapshot.Cash)
	}
	if snapshot.DailyPnL != 15 {
		t.Fatalf("expected broker daily pnl 15, got %.2f", snapshot.DailyPnL)
	}
}

func TestSnapshotUsesBrokerPositionsForCountsAndExposure(t *testing.T) {
	bk := NewBook(stubRuntimeSource{
		summary: &ibkr.AccountSummary{
			NetLiquidation: 1000000,
			Cash:           900000,
		},
		positions: []ibkr.IBKRPosition{
			{Symbol: "AAPL", Quantity: 10, AvgCost: 200},
			{Symbol: "TLT", Quantity: -5, AvgCost: 100},
		},
	}, 1000)

	bk.reconcile(context.Background())
	snapshot := bk.Snapshot()

	if snapshot.OpenPositions != 2 {
		t.Fatalf("expected 2 broker positions, got %d", snapshot.OpenPositions)
	}
	if snapshot.GrossExposure != 2500 {
		t.Fatalf("expected gross exposure 2500, got %.2f", snapshot.GrossExposure)
	}
	if snapshot.NetExposure != 1500 {
		t.Fatalf("expected net exposure 1500, got %.2f", snapshot.NetExposure)
	}
}

func TestReconcileRepairsBookQuantityAndAvgCostFromBroker(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 1000)
	inst := model.Instrument{
		Symbol:   "AAPL",
		SecType:  "STK",
		Currency: "USD",
		Exchange: "SMART",
		ConID:    101,
	}

	pos := bk.OpenPosition(&model.Fill{
		OrderID:    "order-1",
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   10,
		AvgPrice:   100,
		FilledAt:   time.Now(),
	}, &model.Thesis{
		ID:         "thesis-1",
		DeskID:     "desk-1",
		Instrument: inst,
		Direction:  model.Long,
	})

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		ConID:    101,
		Symbol:   "AAPL",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
		Quantity: -12,
		AvgCost:  105.5,
	}})

	if len(discrepancies) != 1 {
		t.Fatalf("expected one discrepancy, got %d", len(discrepancies))
	}
	repaired, ok := bk.GetPosition(pos.ID)
	if !ok {
		t.Fatal("expected repaired position to remain in book")
	}
	if repaired.Direction != model.Short {
		t.Fatalf("expected repaired short direction, got %s", repaired.Direction)
	}
	if repaired.Quantity != 12 {
		t.Fatalf("expected repaired quantity 12, got %.2f", repaired.Quantity)
	}
	if repaired.EntryPrice != 105.5 {
		t.Fatalf("expected repaired entry price 105.5, got %.2f", repaired.EntryPrice)
	}
}

func TestReconcileRecoversBrokerOnlyPositionIntoBook(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 1000)

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		ConID:    202,
		Symbol:   "TLT",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
		Quantity: 5,
		AvgCost:  91.25,
	}})

	if len(discrepancies) != 1 {
		t.Fatalf("expected one discrepancy, got %d", len(discrepancies))
	}

	positions := bk.GetOpenPositions()
	if len(positions) != 1 {
		t.Fatalf("expected one recovered position, got %d", len(positions))
	}
	if positions[0].DeskID != brokerRecoveryDeskID {
		t.Fatalf("expected recovered desk id %q, got %q", brokerRecoveryDeskID, positions[0].DeskID)
	}
	if positions[0].Instrument.Symbol != "TLT" {
		t.Fatalf("expected recovered symbol TLT, got %s", positions[0].Instrument.Symbol)
	}
	if positions[0].EntryPrice != 91.25 {
		t.Fatalf("expected recovered entry price 91.25, got %.2f", positions[0].EntryPrice)
	}
}

func TestReconcileMarksBrokerSyncUnhealthyOnAccountSummaryFailure(t *testing.T) {
	source := &mutableRuntimeSource{
		summaryErr: errors.New("account feed unavailable"),
		positions: []ibkr.IBKRPosition{
			{Symbol: "AAPL", Quantity: 10, AvgCost: 200},
		},
	}
	bk := NewBook(source, 1000)

	bk.reconcile(context.Background())
	status := bk.BrokerSyncStatus()

	if status.Connected {
		t.Fatal("expected broker sync to be marked disconnected")
	}
	if status.LastSynced != (time.Time{}) {
		t.Fatalf("expected LastSynced to remain zero, got %s", status.LastSynced)
	}
	if status.LastPositionsSynced.IsZero() {
		t.Fatal("expected positions sync timestamp to be recorded")
	}
	if status.LastAccountSynced != (time.Time{}) {
		t.Fatalf("expected LastAccountSynced to remain zero, got %s", status.LastAccountSynced)
	}
	if status.LastFailure.IsZero() {
		t.Fatal("expected LastFailure to be recorded")
	}
	if status.ConsecutiveFailures != 1 {
		t.Fatalf("expected one consecutive failure, got %d", status.ConsecutiveFailures)
	}
	if status.LastError == "" || status.LastError != "account_summary: account feed unavailable" {
		t.Fatalf("unexpected LastError: %q", status.LastError)
	}
}

func TestReconcileRecoversBrokerSyncAfterFailure(t *testing.T) {
	source := &mutableRuntimeSource{
		summaryErr:   errors.New("account feed unavailable"),
		positionsErr: errors.New("positions unavailable"),
	}
	bk := NewBook(source, 1000)

	bk.reconcile(context.Background())
	failed := bk.BrokerSyncStatus()
	if failed.ConsecutiveFailures == 0 {
		t.Fatal("expected failure to increment consecutive failures")
	}
	if failed.LastFailure.IsZero() {
		t.Fatal("expected failure timestamp to be recorded")
	}

	source.summaryErr = nil
	source.positionsErr = nil
	source.summary = &ibkr.AccountSummary{
		NetLiquidation: 1000500,
		Cash:           900000,
		UnrealizedPnL:  25,
		RealizedPnL:    10,
	}
	source.positions = []ibkr.IBKRPosition{
		{Symbol: "AAPL", Quantity: 10, AvgCost: 200},
	}

	bk.reconcile(context.Background())
	status := bk.BrokerSyncStatus()

	if !status.Connected {
		t.Fatal("expected broker sync to recover")
	}
	if status.LastSynced.IsZero() {
		t.Fatal("expected LastSynced to be recorded after recovery")
	}
	if status.LastAccountSynced.IsZero() {
		t.Fatal("expected LastAccountSynced to be recorded after recovery")
	}
	if status.LastPositionsSynced.IsZero() {
		t.Fatal("expected LastPositionsSynced to be recorded after recovery")
	}
	if status.ConsecutiveFailures != 0 {
		t.Fatalf("expected failures to reset after recovery, got %d", status.ConsecutiveFailures)
	}
	if status.LastError != "" {
		t.Fatalf("expected LastError to clear after recovery, got %q", status.LastError)
	}
}
