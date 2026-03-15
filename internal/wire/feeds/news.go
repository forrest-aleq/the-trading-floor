package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

// NewsFeed polls RSS feeds for news signals.
type NewsFeed struct {
	log     *slog.Logger
	sources []RSSSource
	http    *http.Client
	states  map[string]*sourceState
}

type RSSSource struct {
	Name     string
	URL      string
	Category string
	Interval time.Duration
}

func DefaultNewsSources() []RSSSource {
	return []RSSSource{
		{Name: "ft-markets", URL: "https://www.ft.com/markets?format=rss", Category: "macro", Interval: 60 * time.Second},
		{Name: "ft-equities", URL: "https://www.ft.com/equities?format=rss", Category: "corporate", Interval: 60 * time.Second},
		{Name: "ft-companies", URL: "https://www.ft.com/companies?format=rss", Category: "corporate", Interval: 60 * time.Second},
		{Name: "ft-world", URL: "https://www.ft.com/world?format=rss", Category: "geopolitical", Interval: 120 * time.Second},
		{Name: "fed-press", URL: "https://www.federalreserve.gov/feeds/press_all.xml", Category: "macro", Interval: 300 * time.Second},
		{Name: "fed-speeches", URL: "https://www.federalreserve.gov/feeds/speeches.xml", Category: "macro", Interval: 300 * time.Second},
	}
}

func NewNewsFeed(sources []RSSSource) *NewsFeed {
	if sources == nil {
		sources = DefaultNewsSources()
	}

	states := make(map[string]*sourceState, len(sources))
	for _, src := range sources {
		states[src.Name] = newSourceState(4096)
	}

	return &NewsFeed{
		log:     slog.Default().With("component", "feed-news"),
		sources: sources,
		http:    newFeedHTTPClient(),
		states:  states,
	}
}

func (f *NewsFeed) Name() string { return "news" }

func (f *NewsFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	for _, src := range f.sources {
		s := src
		go f.pollSource(ctx, s, out)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *NewsFeed) pollSource(ctx context.Context, src RSSSource, out chan<- signal.Signal) {
	ticker := time.NewTicker(src.Interval)
	defer ticker.Stop()

	f.fetchAndSend(ctx, src, out)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.fetchAndSend(ctx, src, out)
		}
	}
}

func (f *NewsFeed) fetchAndSend(ctx context.Context, src RSSSource, out chan<- signal.Signal) {
	state := f.states[src.Name]
	if skip, remaining := state.ShouldPoll(time.Now()); skip {
		f.log.Debug("skipping source during backoff", "source", src.Name, "retry_in", remaining)
		return
	}

	items, err := fetchFeedItems(ctx, f.http, src.URL)
	if err != nil {
		backoff := state.RecordFailure(time.Now(), src.Interval)
		f.log.Warn("rss fetch failed", "source", src.Name, "error", err, "retry_after", backoff)
		return
	}
	state.RecordSuccess()

	for _, item := range items {
		guid := strings.TrimSpace(item.GUID)
		if guid == "" {
			guid = strings.TrimSpace(item.Link)
		}
		if guid == "" {
			guid = strings.TrimSpace(item.Title)
		}
		if guid == "" || state.Seen(guid) {
			continue
		}

		text := strings.TrimSpace(item.Title + " " + item.Description)
		tickers := extractTickers(text)
		entities := make([]signal.Entity, 0, len(tickers))
		for _, ticker := range tickers {
			entities = append(entities, signal.Entity{Name: ticker, Type: "instrument"})
		}

		content, _ := json.Marshal(map[string]string{
			"title":       item.Title,
			"description": item.Description,
			"link":        item.Link,
		})

		sig := signal.Signal{
			ID:         fmt.Sprintf("%s-%s", src.Name, guid),
			Source:     src.Name,
			Type:       signal.TypeNews,
			Category:   src.Category,
			Timestamp:  signalTimestamp(item.PubDate),
			Urgency:    0.5,
			Entities:   entities,
			Raw:        content,
			Translated: text,
			Languages:  []string{"en"},
		}

		select {
		case out <- sig:
		case <-ctx.Done():
			return
		}
	}
}
