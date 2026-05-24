package firm

import (
	"context"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/pkg/model"
)

func TestCEOFactorExposuresAggregateAcrossDesks(t *testing.T) {
	bk := book.NewBook(nil, 1_000_000)
	ceo := NewCEO(bk, nil, nil)
	ceo.SetDesks([]*Desk{
		{ID: "desk-macro-a", Domain: "macro"},
		{ID: "desk-tail-a", Domain: "tail"},
	})

	openTestPosition(t, bk, "desk-macro-a", "tlt-long", "TLT", model.Long, 1000, 100)
	openTestPosition(t, bk, "desk-tail-a", "ief-long", "IEF", model.Long, 1000, 100)

	exposures := ceo.factorExposures(1_000_000, bk.GetOpenPositions())
	rates := requireFactorExposure(t, exposures, "theme:rates_duration")

	if rates.DeskCount != 2 {
		t.Fatalf("expected 2 desks contributing to rates duration, got %d", rates.DeskCount)
	}
	if got := rates.Gross; got != 200_000 {
		t.Fatalf("expected rates gross 200000, got %.2f", got)
	}
	if got := rates.GrossPctNAV; got != 20 {
		t.Fatalf("expected rates gross pct nav 20, got %.2f", got)
	}
	if _, ok := rates.DeskContributions["desk-macro-a"]; !ok {
		t.Fatalf("expected macro desk contribution to be recorded")
	}
	if _, ok := rates.DeskContributions["desk-tail-a"]; !ok {
		t.Fatalf("expected tail desk contribution to be recorded")
	}
}

func TestCEOCrowdedFactorPenaltiesReduceCrowdedDeskWeights(t *testing.T) {
	bk := book.NewBook(nil, 1_000_000)
	ceo := NewCEO(bk, nil, nil)
	ceo.SetDesks([]*Desk{
		{ID: "desk-macro-a", Domain: "macro"},
		{ID: "desk-tail-a", Domain: "tail"},
		{ID: "desk-corp-a", Domain: "corporate"},
	})

	openTestPosition(t, bk, "desk-macro-a", "tlt-crowded", "TLT", model.Long, 2000, 100)
	openTestPosition(t, bk, "desk-tail-a", "ief-crowded", "IEF", model.Long, 2000, 100)
	openTestPosition(t, bk, "desk-corp-a", "xlf-uncrowded", "XLF", model.Long, 500, 100)

	ceo.deskSharpe["desk-macro-a"] = []float64{0, 0, 0, 0, 0}
	ceo.deskSharpe["desk-tail-a"] = []float64{0, 0, 0, 0, 0}
	ceo.deskSharpe["desk-corp-a"] = []float64{0, 0, 0, 0, 0}

	penalties := ceo.crowdedFactorPenalties(1_000_000, bk.GetOpenPositions())
	if penalties["desk-macro-a"] <= 0 {
		t.Fatalf("expected crowded macro desk penalty, got %.4f", penalties["desk-macro-a"])
	}
	if penalties["desk-tail-a"] <= 0 {
		t.Fatalf("expected crowded tail desk penalty, got %.4f", penalties["desk-tail-a"])
	}
	if penalties["desk-corp-a"] != 0 {
		t.Fatalf("expected uncrowded desk penalty 0, got %.4f", penalties["desk-corp-a"])
	}

	ceo.CapitalReallocation()

	snapshot := bk.Snapshot()
	macroCapital := snapshot.DeskCapital["desk-macro-a"]
	tailCapital := snapshot.DeskCapital["desk-tail-a"]
	corpCapital := snapshot.DeskCapital["desk-corp-a"]

	if corpCapital <= macroCapital {
		t.Fatalf("expected uncrowded desk capital %.2f to exceed macro %.2f", corpCapital, macroCapital)
	}
	if corpCapital <= tailCapital {
		t.Fatalf("expected uncrowded desk capital %.2f to exceed tail %.2f", corpCapital, tailCapital)
	}
	if macroCapital != tailCapital {
		t.Fatalf("expected crowded desks to receive equal capital, got macro %.2f tail %.2f", macroCapital, tailCapital)
	}
}

func TestCEOKillSwitchDisablesGlobalEntryControl(t *testing.T) {
	bk := book.NewBook(nil, 1_000_000)
	control := NewManualEntryControl(NormalEntryPolicy(time.Now().UTC()))
	ceo := NewCEO(bk, nil, nil)
	ceo.SetEntryControl(control)

	openTestPosition(t, bk, "desk-macro-a", "drawdown-spy", "SPY", model.Long, 2000, 100)
	bk.Mark(map[string]float64{"SPY": 10})

	ceo.evaluate(context.Background())

	policy := control.CurrentEntryPolicy()
	if policy.AllowEntries {
		t.Fatalf("expected kill switch to disable entries, got %+v", policy)
	}
	if policy.Reason != "ceo_kill_switch" {
		t.Fatalf("expected ceo_kill_switch reason, got %+v", policy)
	}
}

func TestCEOKillSwitchReappliesAfterManualEnable(t *testing.T) {
	bk := book.NewBook(nil, 1_000_000)
	control := NewManualEntryControl(NormalEntryPolicy(time.Now().UTC()))
	ceo := NewCEO(bk, nil, nil)
	ceo.SetEntryControl(control)

	openTestPosition(t, bk, "desk-macro-a", "drawdown-tlt", "TLT", model.Long, 2000, 100)
	bk.Mark(map[string]float64{"TLT": 10})

	ceo.evaluate(context.Background())
	control.Enable(time.Now().UTC())
	ceo.evaluate(context.Background())

	policy := control.CurrentEntryPolicy()
	if policy.AllowEntries {
		t.Fatalf("expected kill switch to re-disable entries, got %+v", policy)
	}
	if policy.Reason != "ceo_kill_switch" {
		t.Fatalf("expected ceo_kill_switch reason, got %+v", policy)
	}
}

func TestFactorSnapshotsPreserveDeskContributionShare(t *testing.T) {
	exposures := []factorExposure{
		{
			Factor:      "theme:rates_duration",
			Gross:       400_000,
			Net:         400_000,
			GrossPctNAV: 40,
			NetPctNAV:   40,
			DeskCount:   2,
			DeskContributions: map[string]factorContribution{
				"desk-a": {DeskID: "desk-a", Domain: "macro", Gross: 300_000, Net: 300_000},
				"desk-b": {DeskID: "desk-b", Domain: "tail", Gross: 100_000, Net: 100_000},
			},
		},
	}

	snapshots := factorSnapshots(exposures)
	if len(snapshots) != 1 {
		t.Fatalf("expected one factor snapshot, got %d", len(snapshots))
	}
	if got := snapshots[0].Contributions[0].GrossShare; got != 0.75 {
		t.Fatalf("expected first contribution share 0.75, got %.2f", got)
	}
	if got := snapshots[0].Contributions[1].GrossShare; got != 0.25 {
		t.Fatalf("expected second contribution share 0.25, got %.2f", got)
	}
}

func TestFactorHistoryPenaltyRequiresRepeatedCrowding(t *testing.T) {
	policy := activeFactorPolicy()

	if got := factorHistoryPenalty(policy, model.PortfolioFactorHistory{
		Factor:             "theme:rates_duration",
		Observations:       policy.HistoryFloorSnapshots - 1,
		AverageGrossPctNAV: 40,
	}); got != 0 {
		t.Fatalf("expected no history penalty before minimum observations, got %.2f", got)
	}

	if got := factorHistoryPenalty(policy, model.PortfolioFactorHistory{
		Factor:             "theme:rates_duration",
		Observations:       policy.HistoryFloorSnapshots + 2,
		AverageGrossPctNAV: 40,
	}); got <= 0 {
		t.Fatalf("expected positive history penalty for repeated crowding, got %.2f", got)
	}
}

func openTestPosition(t *testing.T, bk *book.Book, deskID, id, symbol string, direction model.TradeDirection, qty, price float64) *model.Position {
	t.Helper()

	inst := model.Instrument{
		Symbol:   symbol,
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
	}
	return bk.OpenPosition(&model.Fill{
		OrderID:    id,
		Instrument: inst,
		Direction:  direction,
		Quantity:   qty,
		AvgPrice:   price,
		FilledAt:   time.Unix(1, 0),
	}, &model.Thesis{
		ID:         "thesis-" + id,
		DeskID:     deskID,
		Domain:     deskID,
		Instrument: inst,
		Direction:  direction,
		EntryPrice: price,
	})
}

func requireFactorExposure(t *testing.T, exposures []factorExposure, factor string) factorExposure {
	t.Helper()

	for _, exposure := range exposures {
		if exposure.Factor == factor {
			return exposure
		}
	}
	t.Fatalf("expected factor exposure %s to be present", factor)
	return factorExposure{}
}
