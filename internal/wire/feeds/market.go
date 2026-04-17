package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/marketdata"
	"github.com/hnic/trading-floor/internal/marketrefs"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type MarketDataClient interface {
	Snapshot(context.Context, model.Instrument) (*marketdata.Snapshot, error)
}

// MarketFeed polls market data from IBKR and emits it as wire signals.
type MarketFeed struct {
	log       *slog.Logger
	client    MarketDataClient
	watchlist []model.Instrument
	interval  time.Duration
	states    map[string]*marketSignalState
}

type marketSignalState struct {
	source    *sourceState
	lastPrice float64
	primed    bool
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
	if f.client == nil {
		f.log.Info("market feed disabled; no market state provider configured")
		<-ctx.Done()
		return ctx.Err()
	}

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, inst := range f.watchlist {
				state := f.states[inst.Symbol]
				if skip, remaining := state.source.ShouldPoll(time.Now()); skip {
					f.log.Debug("skipping market symbol during backoff", "symbol", inst.Symbol, "retry_in", remaining)
					continue
				}

				snapshot, err := f.client.Snapshot(ctx, inst)
				if err != nil || snapshot == nil {
					backoff := state.source.RecordFailure(time.Now(), marketDataBackoff(err, f.interval))
					f.log.Warn("market data error", "symbol", inst.Symbol, "error", err, "retry_after", backoff)
					continue
				}
				state.source.RecordSuccess()

				price := bestMarketSignalPrice(snapshot)
				if !state.shouldEmit(price) {
					continue
				}

				raw, err := json.Marshal(map[string]any{
					"symbol": inst.Symbol,
					"last":   snapshot.Last,
					"bid":    snapshot.Bid,
					"ask":    snapshot.Ask,
					"volume": snapshot.Volume,
				})
				if err != nil {
					f.log.Warn("market data marshal failed", "symbol", inst.Symbol, "error", err)
					continue
				}

				timestamp := snapshot.ObservedAt.UTC()
				if timestamp.IsZero() {
					timestamp = time.Now().UTC()
				}

				sig := signal.Signal{
					ID:        fmt.Sprintf("mkt-%s-%d", inst.Symbol, time.Now().UnixMilli()),
					Source:    "market-state",
					Type:      signal.TypePrice,
					Category:  "market",
					Timestamp: timestamp,
					Urgency:   0.3,
					Entities:  []signal.Entity{{Name: inst.Symbol, Type: "instrument"}},
					Raw:       raw,
					Translated: fmt.Sprintf("Market data %s last %.2f bid %.2f ask %.2f volume %d",
						inst.Symbol, snapshot.Last, snapshot.Bid, snapshot.Ask, snapshot.Volume),
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

func marketStates(watchlist []model.Instrument) map[string]*marketSignalState {
	states := make(map[string]*marketSignalState, len(watchlist))
	for _, inst := range watchlist {
		if inst.Symbol == "" {
			continue
		}
		states[inst.Symbol] = &marketSignalState{source: newSourceState(64)}
	}
	return states
}

func (s *marketSignalState) shouldEmit(price float64) bool {
	if price <= 0 {
		return false
	}
	threshold := 0.0075
	if !s.primed {
		s.lastPrice = price
		s.primed = true
		return false
	}
	if s.lastPrice <= 0 {
		s.lastPrice = price
		return false
	}
	change := abs((price - s.lastPrice) / s.lastPrice)
	if change < threshold {
		return false
	}
	s.lastPrice = price
	return true
}

func marketDataBackoff(err error, interval time.Duration) time.Duration {
	if err == nil {
		return interval
	}
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

func bestMarketSignalPrice(snapshot *marketdata.Snapshot) float64 {
	switch {
	case snapshot == nil:
		return 0
	case snapshot.Last > 0:
		return snapshot.Last
	case snapshot.Bid > 0 && snapshot.Ask > 0:
		return (snapshot.Bid + snapshot.Ask) / 2
	case snapshot.Bid > 0:
		return snapshot.Bid
	case snapshot.Ask > 0:
		return snapshot.Ask
	default:
		return 0
	}
}

// DefaultWatchlist returns the explicit market-data signal universe. It is
// intentionally empty by default so desks do not inherit a fake ticker-first
// market wire; thesis-added instruments still enter dynamically.
func DefaultWatchlist() []model.Instrument {
	return marketrefs.MarketSignalWatchlist()
}

// DefaultEarningsWatchlist is a bounded catalyst universe for the earnings
// calendar feed. It is intentionally separate from the market bootstrap set.
func DefaultEarningsWatchlist() []model.Instrument {
	return marketrefs.EarningsWatchlist()
}
