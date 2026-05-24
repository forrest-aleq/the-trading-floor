package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

type AlternativeFeed struct {
	log     *slog.Logger
	client  *http.Client
	sources []AlternativeSource
	states  map[string]*sourceState
}

type AlternativeSource struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category"`
	Format   string `json:"format"` // rss or json
	Interval int    `json:"interval_seconds"`
}

type alternativeItem struct {
	ID          string
	Title       string
	Summary     string
	Link        string
	Symbol      string
	PublishedAt string
}

func defaultAlternativeSourcesFromEnv() []AlternativeSource {
	raw := strings.TrimSpace(os.Getenv("ALT_DATA_SOURCES"))
	if raw == "" {
		return nil
	}

	var sources []AlternativeSource
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		return nil
	}
	return sources
}

func NewAlternativeFeed(sources []AlternativeSource) *AlternativeFeed {
	if sources == nil {
		sources = defaultAlternativeSourcesFromEnv()
	}
	states := make(map[string]*sourceState, len(sources))
	for _, src := range sources {
		states[src.Name] = newSourceState(2048)
	}
	return &AlternativeFeed{
		log:     slog.Default().With("component", "feed-alt"),
		client:  newFeedHTTPClient(),
		sources: sources,
		states:  states,
	}
}

func (f *AlternativeFeed) Name() string { return "alternative" }

func (f *AlternativeFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	if len(f.sources) == 0 {
		f.log.Warn("alternative feed disabled — set ALT_DATA_SOURCES")
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

func (f *AlternativeFeed) pollSource(ctx context.Context, src AlternativeSource, out chan<- signal.Signal) {
	interval := time.Duration(src.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	f.fetchAndSend(ctx, src, out, interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.fetchAndSend(ctx, src, out, interval)
		}
	}
}

func (f *AlternativeFeed) fetchAndSend(ctx context.Context, src AlternativeSource, out chan<- signal.Signal, interval time.Duration) {
	state := f.states[src.Name]
	if skip, remaining := state.ShouldPoll(time.Now()); skip {
		f.log.Debug("skipping alternative source during backoff", "source", src.Name, "retry_in", remaining)
		return
	}

	items, err := f.fetchItems(ctx, src)
	if err != nil {
		backoff := state.RecordFailure(time.Now(), interval)
		f.log.Warn("alternative source fetch failed", "source", src.Name, "error", err, "retry_after", backoff)
		return
	}
	state.RecordSuccess()

	for _, item := range items {
		id := item.ID
		if id == "" {
			id = item.Link
		}
		if id == "" {
			id = fmt.Sprintf("%s-%s", src.Name, item.Title)
		}
		if state.Seen(id) {
			continue
		}

		raw, _ := json.Marshal(map[string]string{
			"title":        item.Title,
			"summary":      item.Summary,
			"link":         item.Link,
			"symbol":       item.Symbol,
			"published_at": item.PublishedAt,
		})

		tickers := extractTickers(strings.TrimSpace(item.Title + " " + item.Summary))
		if symbol := strings.ToUpper(strings.TrimSpace(item.Symbol)); symbol != "" {
			tickers = append([]string{symbol}, tickers...)
		}
		seenTickers := make(map[string]struct{}, len(tickers))
		entities := make([]signal.Entity, 0, len(tickers))
		for _, ticker := range tickers {
			ticker = strings.TrimSpace(strings.ToUpper(ticker))
			if ticker == "" {
				continue
			}
			if _, ok := seenTickers[ticker]; ok {
				continue
			}
			seenTickers[ticker] = struct{}{}
			entities = append(entities, signal.Entity{Name: ticker, Type: "instrument"})
		}

		sig := signal.Signal{
			ID:         fmt.Sprintf("alt-%s-%s", src.Name, id),
			Source:     "alternative/" + src.Name,
			Type:       signal.TypeAlternative,
			Category:   defaultString(strings.TrimSpace(src.Category), "alternative"),
			Timestamp:  signalTimestamp(item.PublishedAt, item.Link, item.Title),
			Urgency:    0.65,
			Entities:   entities,
			Raw:        raw,
			Translated: strings.TrimSpace(item.Title + " " + item.Summary),
		}

		select {
		case out <- sig:
		case <-ctx.Done():
			return
		}
	}
}

func (f *AlternativeFeed) fetchItems(ctx context.Context, src AlternativeSource) ([]alternativeItem, error) {
	switch strings.ToLower(strings.TrimSpace(src.Format)) {
	case "", "json":
		return f.fetchJSONItems(ctx, src.URL)
	case "rss", "atom":
		items, err := fetchFeedItems(ctx, f.client, src.URL)
		if err != nil {
			return nil, err
		}
		out := make([]alternativeItem, 0, len(items))
		for _, item := range items {
			out = append(out, alternativeItem{
				ID:          defaultString(item.GUID, item.Link),
				Title:       item.Title,
				Summary:     item.Description,
				Link:        item.Link,
				PublishedAt: item.PubDate,
			})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported alt format %q", src.Format)
	}
}

func (f *AlternativeFeed) fetchJSONItems(ctx context.Context, sourceURL string) ([]alternativeItem, error) {
	req, err := newFeedRequest(ctx, http.MethodGet, sourceURL)
	if err != nil {
		return nil, err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseAlternativeItems(body)
}

func parseAlternativeItems(body []byte) ([]alternativeItem, error) {
	var direct []map[string]any
	if err := json.Unmarshal(body, &direct); err == nil && len(direct) > 0 {
		return mapAlternativeItems(direct), nil
	}

	var wrapped struct {
		Items []map[string]any `json:"items"`
		Data  []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		switch {
		case len(wrapped.Items) > 0:
			return mapAlternativeItems(wrapped.Items), nil
		case len(wrapped.Data) > 0:
			return mapAlternativeItems(wrapped.Data), nil
		}
	}

	return nil, fmt.Errorf("unexpected alternative payload shape")
}

func mapAlternativeItems(items []map[string]any) []alternativeItem {
	out := make([]alternativeItem, 0, len(items))
	for _, item := range items {
		out = append(out, alternativeItem{
			ID:          firstString(item, "id", "guid"),
			Title:       firstString(item, "title", "headline", "name"),
			Summary:     firstString(item, "summary", "description", "text"),
			Link:        firstString(item, "url", "link"),
			Symbol:      firstString(item, "symbol", "ticker"),
			PublishedAt: firstString(item, "published_at", "timestamp", "date"),
		})
	}
	return out
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := item[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			return text
		}
	}
	return ""
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseIntervalSecondsEnv(name string, fallback int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return time.Duration(fallback) * time.Second
	}
	var seconds int
	if _, err := fmt.Sscanf(raw, "%d", &seconds); err != nil || seconds <= 0 {
		return time.Duration(fallback) * time.Second
	}
	return time.Duration(seconds) * time.Second
}
