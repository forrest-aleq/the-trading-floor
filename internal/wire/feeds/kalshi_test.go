package feeds

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/kalshi"
	"github.com/hnic/trading-floor/pkg/signal"
)

type stubKalshiMarketClient struct {
	responses map[string]*kalshi.MarketsResponse
	cursors   []string
	limits    []int
}

func (s *stubKalshiMarketClient) GetMarkets(_ context.Context, _ string, limit int, cursor string) (*kalshi.MarketsResponse, error) {
	s.cursors = append(s.cursors, cursor)
	s.limits = append(s.limits, limit)
	return s.responses[cursor], nil
}

func TestKalshiFeedPaginatesOpenMarkets(t *testing.T) {
	client := &stubKalshiMarketClient{responses: map[string]*kalshi.MarketsResponse{
		"": {
			Markets: []kalshi.Market{{Ticker: "KXONE"}},
			Cursor:  "next",
		},
		"next": {
			Markets: []kalshi.Market{{Ticker: "KXTWO"}, {Ticker: "KXTHREE"}},
		},
	}}
	feed := NewKalshiFeed(client)
	feed.limit = 2
	feed.maxPages = 3

	markets, pages, cursor, err := feed.fetchOpenMarkets(context.Background())
	if err != nil {
		t.Fatalf("fetchOpenMarkets returned error: %v", err)
	}
	if pages != 2 || cursor != "" || len(markets) != 3 {
		t.Fatalf("unexpected pagination result: pages=%d cursor=%q markets=%+v", pages, cursor, markets)
	}
	if len(client.cursors) != 2 || client.cursors[0] != "" || client.cursors[1] != "next" {
		t.Fatalf("unexpected cursors: %+v", client.cursors)
	}
	if client.limits[0] != 2 || client.limits[1] != 2 {
		t.Fatalf("unexpected limits: %+v", client.limits)
	}
}

func TestKalshiFeedRespectsMaxPages(t *testing.T) {
	client := &stubKalshiMarketClient{responses: map[string]*kalshi.MarketsResponse{
		"": {
			Markets: []kalshi.Market{{Ticker: "KXONE"}},
			Cursor:  "next",
		},
	}}
	feed := NewKalshiFeed(client)
	feed.maxPages = 1

	markets, pages, cursor, err := feed.fetchOpenMarkets(context.Background())
	if err != nil {
		t.Fatalf("fetchOpenMarkets returned error: %v", err)
	}
	if pages != 1 || cursor != "next" || len(markets) != 1 {
		t.Fatalf("unexpected capped result: pages=%d cursor=%q markets=%+v", pages, cursor, markets)
	}
}

func TestMarshalKalshiMarketSignalRawIncludesSportsAvailability(t *testing.T) {
	active := true
	observedAt := time.Date(2026, 6, 26, 18, 0, 0, 0, time.UTC)
	raw, err := marshalKalshiMarketSignalRaw(kalshi.Market{
		Ticker:   "KXGOAL",
		Title:    "Norway vs France: Goalscorer",
		Subtitle: "Erling Haaland: 1+",
	}, SportsAvailabilityEvidence{
		Status:     "confirmed",
		Source:     "espn",
		Player:     "Erling Haaland",
		Team:       "Norway",
		Active:     &active,
		ObservedAt: &observedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Ticker             string                     `json:"ticker"`
		SportsAvailability SportsAvailabilityEvidence `json:"sports_availability"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Ticker != "KXGOAL" || payload.SportsAvailability.Status != "confirmed" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.SportsAvailability.Active == nil || !*payload.SportsAvailability.Active {
		t.Fatalf("expected active availability payload: %+v", payload.SportsAvailability)
	}
}

func TestMarshalKalshiMarketSignalRawPreservesMVELegs(t *testing.T) {
	raw, err := marshalKalshiMarketSignalRaw(kalshi.Market{
		Ticker:              "KXMVESPORTSMULTIGAMEEXTENDED-S202601A7277A770-22D4C50549A",
		MVECollectionTicker: "KXMVESPORTSMULTIGAMEEXTENDED",
		MVESelectedLegs: []kalshi.MVESelectedLeg{{
			EventTicker:  "KXMLBGAME-26JUN262145LADSD",
			MarketTicker: "KXMLBGAME-26JUN262145LADSD-LAD",
			Side:         "yes",
		}},
	}, SportsAvailabilityEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Ticker              string                  `json:"ticker"`
		MVECollectionTicker string                  `json:"mve_collection_ticker"`
		MVESelectedLegs     []kalshi.MVESelectedLeg `json:"mve_selected_legs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MVECollectionTicker != "KXMVESPORTSMULTIGAMEEXTENDED" || len(payload.MVESelectedLegs) != 1 {
		t.Fatalf("expected MVE leg metadata to survive raw marshal, got %+v", payload)
	}
	if payload.MVESelectedLegs[0].MarketTicker != "KXMLBGAME-26JUN262145LADSD-LAD" || payload.MVESelectedLegs[0].Side != "yes" {
		t.Fatalf("unexpected MVE leg payload: %+v", payload.MVESelectedLegs[0])
	}
}

func TestKalshiFeedSkipsMVEWrappersByDefault(t *testing.T) {
	t.Setenv("KALSHI_UNSAFE_ALLOW_MVE_WRAPPERS", "false")
	client := &stubKalshiMarketClient{responses: map[string]*kalshi.MarketsResponse{
		"": {
			Markets: []kalshi.Market{
				{Ticker: "KXMVESPORTSMULTIGAMEEXTENDED-S202601A7277A770-22D4C50549A", YesAskDollars: "0.2400"},
				{Ticker: "KXFEDCUT-26", YesAskDollars: "0.4200"},
			},
		},
	}}
	feed := NewKalshiFeed(client)
	out := make(chan signal.Signal, 2)

	feed.fetchAndSend(context.Background(), out)

	if got := len(out); got != 1 {
		t.Fatalf("expected one non-MVE signal, got %d", got)
	}
	got := <-out
	if got.Entities[0].ID != "KXFEDCUT-26" {
		t.Fatalf("unexpected signal emitted: %+v", got)
	}
}
