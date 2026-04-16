package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

type TelegramFeed struct {
	log     *slog.Logger
	client  *http.Client
	sources []TelegramSource
	states  map[string]*sourceState
}

type TelegramSource struct {
	Name     string
	URL      string
	Category string
	Language string
	Interval time.Duration
}

func defaultTelegramSourcesFromEnv() []TelegramSource {
	rawURLs := strings.TrimSpace(os.Getenv("TELEGRAM_FEED_URLS"))
	if rawURLs == "" {
		return nil
	}

	urls := splitCSV(rawURLs)
	names := splitCSV(os.Getenv("TELEGRAM_FEED_NAMES"))
	category := strings.TrimSpace(os.Getenv("TELEGRAM_FEED_CATEGORY"))
	if category == "" {
		category = "geopolitical"
	}
	language := strings.TrimSpace(os.Getenv("TELEGRAM_FEED_LANGUAGE"))
	if language == "" {
		language = "en"
	}
	interval := parseIntervalSecondsEnv("TELEGRAM_FEED_INTERVAL_SECONDS", 60)

	sources := make([]TelegramSource, 0, len(urls))
	for i, rawURL := range urls {
		name := deriveSourceName(rawURL, "telegram")
		if i < len(names) && strings.TrimSpace(names[i]) != "" {
			name = strings.TrimSpace(names[i])
		}
		sources = append(sources, TelegramSource{
			Name:     name,
			URL:      rawURL,
			Category: category,
			Language: language,
			Interval: interval,
		})
	}
	return sources
}

func NewTelegramFeed(sources []TelegramSource) *TelegramFeed {
	if sources == nil {
		sources = defaultTelegramSourcesFromEnv()
	}
	states := make(map[string]*sourceState, len(sources))
	for _, src := range sources {
		states[src.Name] = newSourceState(2048)
	}
	return &TelegramFeed{
		log:     slog.Default().With("component", "feed-telegram"),
		client:  newFeedHTTPClient(),
		sources: sources,
		states:  states,
	}
}

func (f *TelegramFeed) Name() string { return "telegram" }

func (f *TelegramFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	if len(f.sources) == 0 {
		f.log.Warn("telegram feed disabled — set TELEGRAM_FEED_URLS")
		<-ctx.Done()
		return ctx.Err()
	}

	for _, source := range f.sources {
		src := source
		go f.pollSource(ctx, src, out)
	}

	<-ctx.Done()
	return ctx.Err()
}

func (f *TelegramFeed) pollSource(ctx context.Context, src TelegramSource, out chan<- signal.Signal) {
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

func (f *TelegramFeed) fetchAndSend(ctx context.Context, src TelegramSource, out chan<- signal.Signal) {
	state := f.states[src.Name]
	if skip, remaining := state.ShouldPoll(time.Now()); skip {
		f.log.Debug("skipping telegram source during backoff", "source", src.Name, "retry_in", remaining)
		return
	}

	items, err := fetchFeedItems(ctx, f.client, src.URL)
	if err != nil {
		backoff := state.RecordFailure(time.Now(), src.Interval)
		f.log.Warn("telegram feed fetch failed", "source", src.Name, "error", err, "retry_after", backoff)
		return
	}
	state.RecordSuccess()

	for _, item := range items {
		guid := item.GUID
		if guid == "" {
			guid = item.Link
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

		raw, _ := json.Marshal(map[string]string{
			"title":       item.Title,
			"description": item.Description,
			"link":        item.Link,
			"channel":     src.Name,
		})

		sig := signal.Signal{
			ID:           fmt.Sprintf("telegram-%s-%s", src.Name, guid),
			Source:       "telegram/" + src.Name,
			Type:         signal.TypeSocial,
			Category:     src.Category,
			Timestamp:    signalTimestamp(item.PubDate, item.Link, item.GUID, item.Title),
			Urgency:      0.75,
			Entities:     entities,
			Languages:    []string{src.Language},
			Raw:          raw,
			OriginalText: text,
		}
		if strings.EqualFold(src.Language, "en") {
			sig.Translated = text
			sig.TranslationProvider = "identity"
			sig.TranslationConfidence = 1
		}

		select {
		case out <- sig:
		case <-ctx.Done():
			return
		}
	}
}

func deriveSourceName(rawURL, fallback string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return fallback
	}

	name := strings.Trim(parsed.Host+parsed.Path, "/")
	name = strings.NewReplacer(".", "-", "/", "-").Replace(name)
	if name == "" {
		return fallback
	}
	return name
}
