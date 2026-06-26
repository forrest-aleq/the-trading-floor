package feeds

import (
	"context"
	"testing"

	"github.com/hnic/trading-floor/internal/execution/kalshi"
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
