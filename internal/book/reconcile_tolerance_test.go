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

func TestReconcilePreservesDuplicateDeskPositionsAndAddsBrokerDelta(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)
	inst := model.Instrument{Symbol: "QQQ", SecType: "STK", Exchange: "SMART", Currency: "USD"}
	first := bk.OpenPosition(&model.Fill{
		OrderID:    "desk-short-1",
		Instrument: inst,
		Direction:  model.Short,
		Quantity:   2,
		AvgPrice:   710,
		FilledAt:   time.Now(),
	}, &model.Thesis{
		ID:         "thesis-short-1",
		DeskID:     "sector-tech-a",
		Instrument: inst,
		Direction:  model.Short,
	})
	second := bk.OpenPosition(&model.Fill{
		OrderID:    "desk-short-2",
		Instrument: inst,
		Direction:  model.Short,
		Quantity:   3,
		AvgPrice:   710,
		FilledAt:   time.Now(),
	}, &model.Thesis{
		ID:         "thesis-short-2",
		DeskID:     "sys-momentum-a",
		Instrument: inst,
		Direction:  model.Short,
	})

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		ConID:    320227571,
		Symbol:   "QQQ",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
		Quantity: -10,
		AvgCost:  710,
	}})

	if len(discrepancies) != 1 {
		t.Fatalf("expected aggregate broker quantity drift to be reported, got %+v", discrepancies)
	}
	if discrepancies[0].BookQty != -5 || discrepancies[0].IBKRQty != -10 {
		t.Fatalf("expected aggregate drift -5 -> -10, got %+v", discrepancies[0])
	}
	if first.Quantity != 2 || first.Direction != model.Short {
		t.Fatalf("first desk position was mutated: %+v", first)
	}
	if second.Quantity != 3 || second.Direction != model.Short {
		t.Fatalf("second desk position was mutated: %+v", second)
	}

	var recovery *model.Position
	for _, pos := range bk.GetOpenPositions() {
		if pos.DeskID == brokerRecoveryDeskID {
			recovery = pos
			break
		}
	}
	if recovery == nil {
		t.Fatal("expected broker recovery delta position")
	}
	if recovery.Direction != model.Short || recovery.Quantity != 5 {
		t.Fatalf("expected short recovery delta of 5 shares, got %+v", recovery)
	}
	if got := aggregateSignedQuantity(bk.GetOpenPositions()); got != -10 {
		t.Fatalf("expected aggregate signed QQQ quantity -10, got %.2f", got)
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

func TestRecoveredBrokerUSStockPositionUsesSmartRoute(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		ConID:    131217639,
		Symbol:   "BB",
		SecType:  "STK",
		Exchange: "NYSE",
		Currency: "USD",
		Quantity: 946,
		AvgCost:  3.00,
	}})

	if len(discrepancies) != 1 {
		t.Fatalf("expected recovered broker position to be reported, got %+v", discrepancies)
	}
	var recovered *model.Position
	for _, pos := range bk.GetOpenPositions() {
		if pos.Instrument.Symbol == "BB" {
			recovered = pos
		}
	}
	if recovered == nil {
		t.Fatal("expected recovered BB stock position in book")
	}
	if recovered.Instrument.Exchange != "SMART" {
		t.Fatalf("expected recovered US stock exchange SMART, got %q", recovered.Instrument.Exchange)
	}
	if recovered.Instrument.ConID != 131217639 || recovered.IBKRContractID != 131217639 {
		t.Fatalf("expected broker contract id to be preserved, got instrument=%d position=%d", recovered.Instrument.ConID, recovered.IBKRContractID)
	}
}

func TestRecoveredBrokerUSStockPositionNormalizesRouteFields(t *testing.T) {
	bk := NewBook(stubPositionSource{}, 100000)

	discrepancies := bk.Reconcile([]ibkr.IBKRPosition{{
		ConID:    320227571,
		Symbol:   " QQQ ",
		SecType:  " etf ",
		Exchange: " nasdaq ",
		Currency: " usd ",
		Quantity: 10,
		AvgCost:  710.00,
	}})

	if len(discrepancies) != 1 {
		t.Fatalf("expected recovered broker position to be reported, got %+v", discrepancies)
	}
	var recovered *model.Position
	for _, pos := range bk.GetOpenPositions() {
		if pos.Instrument.Symbol == "QQQ" {
			recovered = pos
		}
	}
	if recovered == nil {
		t.Fatal("expected recovered QQQ position in book")
	}
	if recovered.Instrument.SecType != "STK" || recovered.Instrument.Exchange != "SMART" || recovered.Instrument.Currency != "USD" {
		t.Fatalf("expected normalized STK/SMART/USD route fields, got %+v", recovered.Instrument)
	}
	if recovered.Instrument.ConID != 320227571 || recovered.IBKRContractID != 320227571 {
		t.Fatalf("expected broker contract id to be preserved, got instrument=%d position=%d", recovered.Instrument.ConID, recovered.IBKRContractID)
	}
}
