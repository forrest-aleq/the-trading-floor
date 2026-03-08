package feeds

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

// NewsFeed polls RSS feeds for news signals
type NewsFeed struct {
	log     *slog.Logger
	sources []RSSSource
	http    *http.Client
}

type RSSSource struct {
	Name     string
	URL      string
	Category string
	Interval time.Duration // Poll interval
}

func DefaultNewsSources() []RSSSource {
	return []RSSSource{
		{Name: "reuters-business", URL: "https://feeds.reuters.com/reuters/businessNews", Category: "corporate", Interval: 30 * time.Second},
		{Name: "reuters-markets", URL: "https://feeds.reuters.com/reuters/marketsNews", Category: "macro", Interval: 30 * time.Second},
		{Name: "wsj-markets", URL: "https://feeds.wsj.com/xml/rss/3_7031.xml", Category: "macro", Interval: 60 * time.Second},
		{Name: "ft-markets", URL: "https://www.ft.com/markets?format=rss", Category: "macro", Interval: 60 * time.Second},
		{Name: "cnbc-top", URL: "https://www.cnbc.com/id/100003114/device/rss/rss.html", Category: "corporate", Interval: 60 * time.Second},
		{Name: "sec-edgar", URL: "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=8-K&dateb=&owner=include&count=40&search_text=&start=0&output=atom", Category: "filing", Interval: 60 * time.Second},
		{Name: "fed-speeches", URL: "https://www.federalreserve.gov/feeds/speeches.xml", Category: "macro", Interval: 300 * time.Second},
	}
}

func NewNewsFeed(sources []RSSSource) *NewsFeed {
	if sources == nil {
		sources = DefaultNewsSources()
	}
	return &NewsFeed{
		log:     slog.Default().With("component", "feed-news"),
		sources: sources,
		http:    &http.Client{Timeout: 15 * time.Second},
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
	seen := make(map[string]bool) // Track seen GUIDs to avoid re-sending

	ticker := time.NewTicker(src.Interval)
	defer ticker.Stop()

	// Initial fetch
	f.fetchAndSend(ctx, src, out, seen)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.fetchAndSend(ctx, src, out, seen)
		}
	}
}

func (f *NewsFeed) fetchAndSend(ctx context.Context, src RSSSource, out chan<- signal.Signal, seen map[string]bool) {
	items, err := f.fetchRSS(ctx, src.URL)
	if err != nil {
		f.log.Warn("rss fetch failed", "source", src.Name, "error", err)
		return
	}

	for _, item := range items {
		guid := item.GUID
		if guid == "" {
			guid = item.Link
		}
		if seen[guid] {
			continue
		}
		seen[guid] = true

		content, _ := json.Marshal(map[string]string{
			"title":       item.Title,
			"description": item.Description,
			"link":        item.Link,
		})

		sig := signal.Signal{
			ID:        fmt.Sprintf("%s-%s", src.Name, guid),
			Source:    src.Name,
			Type:      signal.TypeNews,
			Category:  src.Category,
			Timestamp: time.Now(),
			Urgency:   0.5, // Default; scanner will re-score
			Raw:       content,
			Languages: []string{"en"},
		}

		select {
		case out <- sig:
		case <-ctx.Done():
			return
		}
	}
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
}

func (f *NewsFeed) fetchRSS(ctx context.Context, url string) ([]rssItem, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TradingFloor/1.0")

	resp, err := f.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Try RSS 2.0
	var rss struct {
		Channel struct {
			Items []rssItem `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		return rss.Channel.Items, nil
	}

	// Try Atom
	var atom struct {
		Entries []struct {
			Title   string `xml:"title"`
			Link    struct{ Href string `xml:"href,attr"` } `xml:"link"`
			Summary string `xml:"summary"`
			ID      string `xml:"id"`
			Updated string `xml:"updated"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &atom); err == nil {
		items := make([]rssItem, len(atom.Entries))
		for i, e := range atom.Entries {
			items[i] = rssItem{
				Title:       e.Title,
				Link:        e.Link.Href,
				Description: e.Summary,
				GUID:        e.ID,
				PubDate:     e.Updated,
			}
		}
		return items, nil
	}

	return nil, fmt.Errorf("could not parse RSS or Atom feed")
}
