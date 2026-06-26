package main

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/observe"
	"github.com/hnic/trading-floor/pkg/model"
)

// instrumentWatchlist replaces the market-data overlay owned by open positions.
type instrumentWatchlist interface {
	SetOpenPositionInstruments([]model.Instrument)
}

// startOpenPositionMarketDataSync keeps live book positions registered for mark updates.
func startOpenPositionMarketDataSync(ctx context.Context, bk *book.Book, watchlist instrumentWatchlist, interval time.Duration) {
	if bk == nil || watchlist == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}

	log := slog.Default().With("component", "position_mark_watchlist")
	syncOnce := func() {
		instruments := openPositionMarketDataInstruments(bk.GetOpenPositions())
		watchlist.SetOpenPositionInstruments(instruments)
		log.Debug("open position market data watchlist synced", "instruments", len(instruments))
	}

	observe.SafeGo(log, "open position market data watchlist sync panic", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		select {
		case <-ctx.Done():
			return
		default:
			syncOnce()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncOnce()
			}
		}
	}, "task", "position_mark_watchlist", "interval", interval.String())
}

// openPositionMarketDataInstruments returns the unique live instruments that require marks.
func openPositionMarketDataInstruments(positions []*model.Position) []model.Instrument {
	byKey := make(map[string]model.Instrument)
	for _, pos := range positions {
		if pos == nil || pos.Status != "open" || pos.Shadow {
			continue
		}
		if pos.IsMultiLeg() {
			for _, leg := range pos.Legs {
				addMarketDataInstrument(byKey, leg.Instrument)
			}
			continue
		}
		addMarketDataInstrument(byKey, pos.Instrument)
	}

	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	instruments := make([]model.Instrument, 0, len(keys))
	for _, key := range keys {
		instruments = append(instruments, byKey[key])
	}
	return instruments
}

// addMarketDataInstrument normalizes an instrument before adding it to a watchlist set.
func addMarketDataInstrument(byKey map[string]model.Instrument, inst model.Instrument) {
	inst.Symbol = strings.TrimSpace(inst.Symbol)
	if inst.Symbol == "" {
		return
	}
	if inst.SecType == "" {
		inst.SecType = "STK"
	}
	if inst.Currency == "" {
		inst.Currency = "USD"
	}
	if inst.Exchange == "" {
		inst.Exchange = "SMART"
	}
	byKey[inst.Key()] = inst
}
