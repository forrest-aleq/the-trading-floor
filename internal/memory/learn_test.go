package memory

import (
	"testing"

	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/pkg/model"
)

func TestLearnWorkerRecordsGlobalAndDeskEngrams(t *testing.T) {
	graph := belief.NewGraph()
	store := NewEngramStore()
	worker := NewLearnWorker(graph, store)

	thesis := &model.Thesis{
		ID:           "thesis-1",
		DeskID:       "desk-a",
		Strategy:     "macro",
		EntryPrice:   100,
		StopLoss:     95,
		PositionSize: 10,
		Instrument: model.Instrument{
			Symbol:   "AAPL",
			SecType:  "STK",
			Currency: "USD",
		},
	}
	outcome := &model.ThesisOutcome{
		Profitable:  true,
		RealizedPnL: 200,
	}
	regime := model.Regime{
		Volatility: "medium",
		Trend:      "neutral",
		Risk:       "risk_on",
		Liquidity:  "normal",
	}

	worker.ProcessOutcome(thesis, outcome, regime)

	stats := store.Stats()
	if stats.Total != 2 {
		t.Fatalf("expected 2 engrams, got %+v", stats)
	}
	if stats.Global != 1 || stats.DeskSpecific != 1 {
		t.Fatalf("expected 1 global and 1 desk-specific engram, got %+v", stats)
	}

	engrams := store.Lookup("macro_STK", "desk-a")
	if len(engrams) != 2 {
		t.Fatalf("expected desk lookup to include desk + global engrams, got %d", len(engrams))
	}
	if engrams[0].DeskID != "desk-a" {
		t.Fatalf("expected desk-specific engram first, got desk_id=%q", engrams[0].DeskID)
	}
	if engrams[1].Layer != 1 {
		t.Fatalf("expected global engram second, got layer=%d", engrams[1].Layer)
	}
}
