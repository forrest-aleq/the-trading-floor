package main

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

// TestOpenPositionMarketDataInstrumentsUsesLiveOpenPositions verifies live mark subscriptions.
func TestOpenPositionMarketDataInstrumentsUsesLiveOpenPositions(t *testing.T) {
	positions := []*model.Position{
		{
			ID:         "qqq-live",
			Status:     "open",
			Instrument: model.Instrument{Symbol: "QQQ", SecType: "STK", Exchange: "NASDAQ", Currency: "USD", ConID: 320227571},
		},
		{
			ID:         "qqq-duplicate",
			Status:     "open",
			Instrument: model.Instrument{Symbol: "QQQ", SecType: "STK", Exchange: "NASDAQ", Currency: "USD", ConID: 320227571},
		},
		{
			ID:         "shadow",
			Status:     "open",
			Shadow:     true,
			Instrument: model.Instrument{Symbol: "NFLX", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		},
		{
			ID:         "closed",
			Status:     "closed",
			Instrument: model.Instrument{Symbol: "MSFT", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		},
		{
			ID:        "spread",
			Status:    "open",
			Structure: "vertical",
			Legs: []model.TradeLeg{
				{Instrument: model.Instrument{Symbol: "SPY", SecType: "OPT", Exchange: "SMART", Currency: "USD", Expiry: "20270115", Strike: 700, Right: "C"}},
				{Instrument: model.Instrument{Symbol: "SPY", SecType: "OPT", Exchange: "SMART", Currency: "USD", Expiry: "20270115", Strike: 710, Right: "C"}},
			},
		},
		{
			ID:         "defaulted",
			Status:     "open",
			Instrument: model.Instrument{Symbol: " AAPL "},
		},
	}

	instruments := openPositionMarketDataInstruments(positions)
	got := map[string]model.Instrument{}
	symbolCounts := map[string]int{}
	for _, inst := range instruments {
		got[inst.Key()] = inst
		symbolCounts[inst.Symbol]++
	}

	if len(got) != 4 {
		t.Fatalf("expected 4 unique live instruments, got %d: %#v", len(got), got)
	}
	if symbolCounts["QQQ"] != 1 {
		t.Fatalf("expected one deduplicated QQQ instrument, got counts %#v", symbolCounts)
	}
	if symbolCounts["NFLX"] > 0 {
		t.Fatalf("shadow position leaked into watchlist: %#v", got)
	}
	if symbolCounts["MSFT"] > 0 {
		t.Fatalf("closed position leaked into watchlist: %#v", got)
	}
	var aapl model.Instrument
	for _, inst := range instruments {
		if inst.Symbol == "AAPL" {
			aapl = inst
		}
	}
	if aapl.Exchange != "SMART" || aapl.Currency != "USD" || aapl.SecType != "STK" {
		t.Fatalf("expected defaulted AAPL instrument, got %#v", aapl)
	}
	if !containsOptionStrike(instruments, "SPY", 700) {
		t.Fatalf("expected long option leg in watchlist, got %#v", got)
	}
	if !containsOptionStrike(instruments, "SPY", 710) {
		t.Fatalf("expected short option leg in watchlist, got %#v", got)
	}
}

// containsOptionStrike reports whether an option leg made it into the watchlist.
func containsOptionStrike(instruments []model.Instrument, symbol string, strike float64) bool {
	for _, inst := range instruments {
		if inst.Symbol == symbol && inst.SecType == "OPT" && inst.Strike == strike {
			return true
		}
	}
	return false
}
