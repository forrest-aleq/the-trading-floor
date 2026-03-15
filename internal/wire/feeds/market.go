package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type MarketDataClient interface {
	ReqMarketData(context.Context, model.Instrument) (*ibkr.MarketData, error)
}

// MarketFeed polls market data from IBKR and emits it as wire signals.
type MarketFeed struct {
	log       *slog.Logger
	client    MarketDataClient
	watchlist []model.Instrument
	interval  time.Duration
	states    map[string]*sourceState
}

func NewMarketFeed(client MarketDataClient, watchlist []model.Instrument) *MarketFeed {
	if watchlist == nil {
		watchlist = DefaultWatchlist()
	}

	return &MarketFeed{
		log:       slog.Default().With("component", "feed-market"),
		client:    client,
		watchlist: watchlist,
		interval:  30 * time.Second,
		states:    marketStates(watchlist),
	}
}

func (f *MarketFeed) Name() string { return "market" }

func (f *MarketFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, inst := range f.watchlist {
				state := f.states[inst.Symbol]
				if skip, remaining := state.ShouldPoll(time.Now()); skip {
					f.log.Debug("skipping market symbol during backoff", "symbol", inst.Symbol, "retry_in", remaining)
					continue
				}

				md, err := f.client.ReqMarketData(ctx, inst)
				if err != nil {
					backoff := state.RecordFailure(time.Now(), marketDataBackoff(err, f.interval))
					f.log.Warn("market data error", "symbol", inst.Symbol, "error", err, "retry_after", backoff)
					continue
				}
				state.RecordSuccess()

				raw, err := json.Marshal(map[string]any{
					"symbol": inst.Symbol,
					"last":   md.Last,
					"bid":    md.Bid,
					"ask":    md.Ask,
					"volume": md.Volume,
				})
				if err != nil {
					f.log.Warn("market data marshal failed", "symbol", inst.Symbol, "error", err)
					continue
				}

				sig := signal.Signal{
					ID:        fmt.Sprintf("mkt-%s-%d", inst.Symbol, time.Now().UnixMilli()),
					Source:    "ibkr-market",
					Type:      signal.TypePrice,
					Category:  "market",
					Timestamp: time.UnixMilli(md.Timestamp).UTC(),
					Urgency:   0.3,
					Entities:  []signal.Entity{{Name: inst.Symbol, Type: "instrument"}},
					Raw:       raw,
					Translated: fmt.Sprintf("Market data %s last %.2f bid %.2f ask %.2f volume %d",
						inst.Symbol, md.Last, md.Bid, md.Ask, md.Volume),
				}

				select {
				case out <- sig:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

func marketStates(watchlist []model.Instrument) map[string]*sourceState {
	states := make(map[string]*sourceState, len(watchlist))
	for _, inst := range watchlist {
		if inst.Symbol == "" {
			continue
		}
		states[inst.Symbol] = newSourceState(64)
	}
	return states
}

func marketDataBackoff(err error, interval time.Duration) time.Duration {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not subscribed"),
		strings.Contains(message, "additional subscription"),
		strings.Contains(message, "delayed market data is available"):
		return 10 * time.Minute
	default:
		return interval
	}
}

func DefaultWatchlist() []model.Instrument {
	return []model.Instrument{
		{Symbol: "SPY", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "QQQ", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "IWM", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "DIA", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "VIX", SecType: "IND", Exchange: "CBOE", Currency: "USD"},
		{Symbol: "AAPL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "MSFT", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "NVDA", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "AMZN", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "GOOGL", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "GLD", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "USO", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "TLT", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "XLE", SecType: "STK", Exchange: "SMART", Currency: "USD"},
		{Symbol: "XLF", SecType: "STK", Exchange: "SMART", Currency: "USD"},
	}
}
