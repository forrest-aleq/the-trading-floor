package book

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
)

func openReconcileTestPosition(bk *Book, qty, avgPrice float64) {
	inst := model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"}
	bk.OpenPosition(&model.Fill{
		OrderID:    "rec-1",
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   qty,
		AvgPrice:   avgPrice,
		FilledAt:   time.Now(),
	}, &model.Thesis{
		ID:         "thesis-rec-1",
		DeskID:     "desk-1",
		Instrument: inst,
		Direction:  model.Long,
	})
}

func openReconcileOptionPosition(bk *Book, premium float64) *model.Position {
	inst := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   "20270115",
		Strike:   140,
		Right:    "C",
	}
	return bk.OpenPosition(&model.Fill{
		OrderID:    "rec-opt",
		Instrument: inst,
		Direction:  model.Long,
		Quantity:   2,
		AvgPrice:   premium,
		FilledAt:   time.Now(),
	}, &model.Thesis{
		ID:         "thesis-rec-opt",
		DeskID:     "desk-1",
		Instrument: inst,
		Direction:  model.Long,
	})
}

func TestReconcileIgnoresFloatNoiseDiscrepancies(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	openReconcileTestPosition(bk, 3, 251.43000000000003)

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		Symbol:   "AAPL",
		Quantity: 3.0000000000000004,
		AvgCost:  251.43,
	}})

	if len(discrepancies) != 0 {
		t.Fatalf("expected float noise to be tolerated, got %+v", discrepancies)
	}
}

func TestReconcileStillFlagsRealDrift(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	openReconcileTestPosition(bk, 3, 251.43)

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		Symbol:   "AAPL",
		Quantity: 3,
		AvgCost:  253.10,
	}})

	if len(discrepancies) != 1 {
		t.Fatalf("expected real avg-cost drift to be flagged, got %+v", discrepancies)
	}
}

// IBKR reports option avgCost per contract (premium × multiplier, plus fees);
// the book stores per-share premiums. A matched option position must not be
// flagged — or worse, "repaired" to a 100x entry price.
func TestReconcileAcceptsPerContractOptionAvgCost(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	pos := openReconcileOptionPosition(bk, 2.50)

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Quantity: 2,
		AvgCost:  250.00, // 2.50 premium × 100 multiplier
	}})

	if len(discrepancies) != 0 {
		t.Fatalf("expected per-contract option avgCost to match book premium, got %+v", discrepancies)
	}
	if pos.EntryPrice != 2.50 {
		t.Fatalf("expected entry price to remain per-share 2.50, got %.4f", pos.EntryPrice)
	}
}

func TestReconcileRepairsOptionDriftToPerSharePremium(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	pos := openReconcileOptionPosition(bk, 2.50)

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Quantity: 2,
		AvgCost:  261.30, // real drift: broker says 2.613 per share
	}})

	if len(discrepancies) != 1 {
		t.Fatalf("expected option avg-cost drift to be flagged, got %+v", discrepancies)
	}
	if pos.EntryPrice < 2.612 || pos.EntryPrice > 2.614 {
		t.Fatalf("expected repair to per-share 2.613, got %.4f", pos.EntryPrice)
	}
}

func TestRecoveredBrokerOptionPositionUsesPerSharePremium(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		Symbol:   "AMD",
		SecType:  "OPT",
		Quantity: 1,
		AvgCost:  250.00,
	}})

	if len(discrepancies) != 1 {
		t.Fatalf("expected recovered broker position to be reported, got %+v", discrepancies)
	}
	var recovered *model.Position
	for _, pos := range bk.GetOpenPositions() {
		if pos.Instrument.Symbol == "AMD" {
			recovered = pos
		}
	}
	if recovered == nil {
		t.Fatal("expected recovered AMD option position in book")
	}
	if recovered.EntryPrice < 2.49 || recovered.EntryPrice > 2.51 {
		t.Fatalf("expected recovered entry price per-share 2.50, got %.4f", recovered.EntryPrice)
	}
}
