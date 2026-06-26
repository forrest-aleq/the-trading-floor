package firm

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution"
	kalshiexec "github.com/hnic/trading-floor/internal/execution/kalshi"
	"github.com/hnic/trading-floor/pkg/model"
)

func TestKalshiPersistedOrderRecordsDryRunWithoutPositionSemantics(t *testing.T) {
	thesis := &model.Thesis{
		ID:         "thesis-1",
		DeskID:     "kalshi-macro-a",
		Domain:     "prediction_market",
		Instrument: model.NormalizeKalshiInstrument(model.Instrument{Symbol: "KXTEST-26"}),
		Direction:  model.Long,
		EntryPrice: 0.24,
		MarketContext: &model.MarketContext{
			CurrentPrice: 0.24,
			BidPrice:     0.23,
			AskPrice:     0.24,
			MidPrice:     0.235,
			SpreadBps:    425.53,
		},
	}
	result := &kalshiexec.ExecutionResult{
		Mode:   kalshiexec.ExecutionDryRun,
		DryRun: true,
		MappedOrder: kalshiexec.MappedOrder{
			Request: kalshiexec.OrderRequest{
				Ticker:          "KXTEST-26",
				ClientOrderID:   "tf-thesis-1",
				Side:            "yes",
				Action:          "buy",
				Count:           4,
				YesPriceDollars: "0.2400",
				TimeInForce:     "fill_or_kill",
			},
			EstimatedRiskCents: 96,
			MaxOrderCents:      100,
			ThesisID:           thesis.ID,
			DeskID:             thesis.DeskID,
			Direction:          string(model.Long),
			ContractIntent:     "buy_yes",
		},
		RecordedAt: time.Date(2026, 5, 24, 5, 46, 0, 0, time.UTC),
	}

	record, ok := kalshiPersistedOrder(thesis, result)
	if !ok {
		t.Fatal("expected persisted Kalshi order")
	}
	if record.Snapshot.State != execution.OrderStateDryRun {
		t.Fatalf("state = %q", record.Snapshot.State)
	}
	if record.Snapshot.IsWorking() {
		t.Fatal("dry-run mapped order must not hydrate as a live working order")
	}
	if !record.Snapshot.Paper || record.Snapshot.Venue != "kalshi" {
		t.Fatalf("unexpected snapshot paper/venue: %+v", record.Snapshot)
	}
	if record.Snapshot.Quantity != 4 || record.Snapshot.FilledQuantity != 0 {
		t.Fatalf("unexpected quantity/fill: %+v", record.Snapshot)
	}
	if record.Fill != nil {
		t.Fatalf("dry-run should not synthesize a fill: %+v", record.Fill)
	}
	if record.Order.ID != "tf-thesis-1" || record.Order.Instrument.SecType != model.SecTypeKalshi {
		t.Fatalf("unexpected order: %+v", record.Order)
	}
	if record.Order.Notional != 0.96 {
		t.Fatalf("expected risk notional 0.96, got %.2f", record.Order.Notional)
	}
	if record.Order.ExecutionIntent == nil || record.Order.ExecutionIntent.BidPrice != 0.23 {
		t.Fatalf("expected market context on execution intent, got %+v", record.Order.ExecutionIntent)
	}
}

func TestKalshiPersistedOrderRecordsLiveFill(t *testing.T) {
	thesis := &model.Thesis{
		ID:         "thesis-2",
		DeskID:     "kalshi-tech-a",
		Domain:     "prediction_market",
		Instrument: model.NormalizeKalshiInstrument(model.Instrument{Symbol: "KXTEST-26"}),
		Direction:  model.Short,
		EntryPrice: 0.22,
	}
	result := &kalshiexec.ExecutionResult{
		Mode:   kalshiexec.ExecutionLive,
		DryRun: false,
		MappedOrder: kalshiexec.MappedOrder{
			Request: kalshiexec.OrderRequest{
				Ticker:         "KXTEST-26",
				ClientOrderID:  "tf-thesis-2",
				Side:           "no",
				Action:         "buy",
				Count:          4,
				NoPriceDollars: "0.2200",
				TimeInForce:    "fill_or_kill",
			},
			EstimatedRiskCents: 88,
			MaxOrderCents:      100,
			ThesisID:           thesis.ID,
			DeskID:             thesis.DeskID,
			Direction:          string(model.Short),
			ContractIntent:     "buy_no",
		},
		Response: &kalshiexec.OrderResponse{
			OrderID:       "kalshi-order-1",
			ClientOrderID: "tf-thesis-2",
			Status:        "executed",
			FillCountFP:   "4",
		},
		RecordedAt: time.Date(2026, 5, 24, 5, 47, 0, 0, time.UTC),
	}

	record, ok := kalshiPersistedOrder(thesis, result)
	if !ok {
		t.Fatal("expected persisted Kalshi order")
	}
	if record.Snapshot.State != execution.OrderStateFilled {
		t.Fatalf("state = %q", record.Snapshot.State)
	}
	if record.Snapshot.VenueOrderID != "kalshi-order-1" || record.Snapshot.FilledQuantity != 4 {
		t.Fatalf("unexpected venue order/fill fields: %+v", record.Snapshot)
	}
	if record.Snapshot.ExecutionQuality.FillRatio != 1 {
		t.Fatalf("expected full fill ratio, got %.2f", record.Snapshot.ExecutionQuality.FillRatio)
	}
	if record.Fill == nil || record.Fill.Quantity != 4 || record.Fill.AvgPrice != 0.22 {
		t.Fatalf("expected live fill payload, got %+v", record.Fill)
	}
}

func TestKalshiEntryWorkShouldPauseOnlyForLivePredictionDeskWithZeroCapacity(t *testing.T) {
	if !kalshiEntryWorkShouldPause(true, true, false, 0, false) {
		t.Fatal("expected live prediction desk with zero capacity to pause new entry work")
	}
	if kalshiEntryWorkShouldPause(true, true, true, 0, false) {
		t.Fatal("dry-run Kalshi desks should not pause on live-capacity state")
	}
	if kalshiEntryWorkShouldPause(false, true, false, 0, false) {
		t.Fatal("non-prediction desks should not pause on Kalshi capacity")
	}
	if kalshiEntryWorkShouldPause(true, false, false, 0, false) {
		t.Fatal("missing executor should follow the existing unavailable-executor path")
	}
	if kalshiEntryWorkShouldPause(true, true, false, 100, true) {
		t.Fatal("positive live capacity should not pause new entry work")
	}
}
