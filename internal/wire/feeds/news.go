package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

// NewsFeed polls RSS feeds for news signals.
type NewsFeed struct {
	log             *slog.Logger
	sources         []RSSSource
	http            *http.Client
	states          map[string]*sourceState
	maxItemsPerPoll int
	maxItemAge      time.Duration
}

type RSSSource struct {
	Name     string
	URL      string
	Category string
	Language string
	Interval time.Duration
}

func DefaultNewsSources() []RSSSource {
	sources := []RSSSource{
		{Name: "ft-markets", URL: "https://www.ft.com/markets?format=rss", Category: "macro", Language: "en", Interval: 60 * time.Second},
		{Name: "ft-equities", URL: "https://www.ft.com/equities?format=rss", Category: "corporate", Language: "en", Interval: 60 * time.Second},
		{Name: "ft-companies", URL: "https://www.ft.com/companies?format=rss", Category: "corporate", Language: "en", Interval: 60 * time.Second},
		{Name: "ft-world", URL: "https://www.ft.com/world?format=rss", Category: "geopolitical", Language: "en", Interval: 120 * time.Second},
		{Name: "fed-press", URL: "https://www.federalreserve.gov/feeds/press_all.xml", Category: "macro", Language: "en", Interval: 300 * time.Second},
		{Name: "fed-speeches", URL: "https://www.federalreserve.gov/feeds/speeches.xml", Category: "macro", Language: "en", Interval: 300 * time.Second},
	}
	return append(sources, extraNewsSourcesFromEnv()...)
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
		log:             slog.Default().With("component", "feed-news"),
		sources:         sources,
		http:            newFeedHTTPClient(),
		states:          states,
		maxItemsPerPoll: readFeedInt("WIRE_NEWS_MAX_ITEMS_PER_SOURCE", 5),
		maxItemAge:      readFeedDuration("WIRE_NEWS_MAX_ITEM_AGE", 6*time.Hour),
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
	emitted := 0
	now := time.Now()

	for _, item := range items {
		publishedAt := signalTimestamp(item.PubDate)
		if f.maxItemAge > 0 && now.Sub(publishedAt) > f.maxItemAge {
			continue
		}

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
			ID:           fmt.Sprintf("%s-%s", src.Name, guid),
			Source:       src.Name,
			Type:         signal.TypeNews,
			Category:     src.Category,
			Timestamp:    publishedAt,
			Urgency:      0.5,
			Entities:     entities,
			Raw:          content,
			OriginalText: text,
			Languages:    []string{firstNonEmptyLocal(strings.TrimSpace(src.Language), "en")},
		}
		if sig.Languages[0] == "en" {
			sig.Translated = text
			sig.TranslationProvider = "identity"
			sig.TranslationConfidence = 1
		}

		select {
		case out <- sig:
			emitted++
			if f.maxItemsPerPoll > 0 && emitted >= f.maxItemsPerPoll {
				f.log.Info("rss source burst capped", "source", src.Name, "emitted", emitted, "max_items", f.maxItemsPerPoll)
				return
			}
		case <-ctx.Done():
			return
		}
	}

	if emitted > 0 {
		f.log.Info("rss source emitted signals", "source", src.Name, "count", emitted)
	}
}

func extraNewsSourcesFromEnv() []RSSSource {
	raw := strings.TrimSpace(os.Getenv("WIRE_NEWS_EXTRA_SOURCES_JSON"))
	if raw == "" {
		return nil
	}

	var payload []struct {
		Name            string `json:"name"`
		URL             string `json:"url"`
		Category        string `json:"category"`
		Language        string `json:"language"`
		IntervalSeconds int    `json:"interval_seconds"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		slog.Default().With("component", "feed-news").Warn("invalid WIRE_NEWS_EXTRA_SOURCES_JSON", "error", err)
		return nil
	}

	sources := make([]RSSSource, 0, len(payload))
	for _, item := range payload {
		if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.URL) == "" {
			continue
		}
		interval := time.Duration(item.IntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 90 * time.Second
		}
		sources = append(sources, RSSSource{
			Name:     strings.TrimSpace(item.Name),
			URL:      strings.TrimSpace(item.URL),
			Category: firstNonEmptyLocal(strings.TrimSpace(item.Category), "geopolitical"),
			Language: firstNonEmptyLocal(strings.TrimSpace(item.Language), "en"),
			Interval: interval,
		})
	}
	return sources
}

func firstNonEmptyLocal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
