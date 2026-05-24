package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

// SocialFeed monitors social media and sentiment sources for trading signals.
// Polls Reddit (pushshift) and StockTwits for high-engagement posts
// mentioning tickers.
type SocialFeed struct {
	log               *slog.Logger
	client            *http.Client
	interval          time.Duration
	states            map[string]*sourceState
	maxStockTwitsPoll int
	maxRedditPoll     int
}

func NewSocialFeed() *SocialFeed {
	return &SocialFeed{
		log:               slog.Default().With("component", "feed-social"),
		client:            newFeedHTTPClient(),
		interval:          2 * time.Minute,
		maxStockTwitsPoll: readFeedInt("WIRE_SOCIAL_MAX_STOCKTWITS_ITEMS", 4),
		maxRedditPoll:     readFeedInt("WIRE_SOCIAL_MAX_REDDIT_ITEMS", 3),
		states: map[string]*sourceState{
			"stocktwits":            newSourceState(2048),
			"reddit/wallstreetbets": newSourceState(2048),
			"reddit/stocks":         newSourceState(2048),
		},
	}
}

func (f *SocialFeed) Name() string { return "social" }

func (f *SocialFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	f.poll(ctx, out)

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			f.poll(ctx, out)
		}
	}
}

func (f *SocialFeed) poll(ctx context.Context, out chan<- signal.Signal) {
	// StockTwits trending
	f.pollStockTwits(ctx, out)
	// Reddit wallstreetbets / stocks
	f.pollReddit(ctx, out, "wallstreetbets")
	f.pollReddit(ctx, out, "stocks")
}

// pollStockTwits fetches trending symbols from StockTwits
func (f *SocialFeed) pollStockTwits(ctx context.Context, out chan<- signal.Signal) {
	state := f.states["stocktwits"]
	if skip, remaining := state.ShouldPoll(time.Now()); skip {
		f.log.Debug("skipping stocktwits during backoff", "retry_in", remaining)
		return
	}

	req, err := newFeedRequest(ctx, http.MethodGet, "https://api.stocktwits.com/api/2/trending/symbols.json")
	if err != nil {
		return
	}

	resp, err := f.client.Do(req)
	if err != nil {
		backoff := state.RecordFailure(time.Now(), f.interval)
		f.log.Warn("stocktwits fetch failed", "error", err, "retry_after", backoff)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		backoff := state.RecordFailure(time.Now(), f.interval)
		f.log.Warn("stocktwits non-200", "status", resp.StatusCode, "retry_after", backoff)
		return
	}
	state.RecordSuccess()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var result struct {
		Symbols []struct {
			Symbol string `json:"symbol"`
			Title  string `json:"title"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return
	}

	newCount := 0
	for _, sym := range result.Symbols {
		symbol := strings.ToUpper(strings.TrimSpace(sym.Symbol))
		if len(symbol) == 0 || len(symbol) > 5 || !isAlpha(symbol) {
			continue
		}

		id := fmt.Sprintf("stocktwits-trending-%s-%s", symbol, time.Now().Format("2006-01-02-15"))
		if state.Seen(id) {
			continue
		}
		newCount++

		raw, _ := json.Marshal(map[string]string{
			"source": "stocktwits",
			"symbol": symbol,
			"title":  sym.Title,
			"type":   "trending",
		})

		sig := signal.Signal{
			ID:         id,
			Source:     "stocktwits",
			Type:       signal.TypeSocial,
			Category:   "flows",
			Timestamp:  time.Now(),
			Urgency:    0.4,
			Entities:   []signal.Entity{{Name: symbol, Type: "instrument"}},
			Raw:        raw,
			Translated: fmt.Sprintf("StockTwits: %s (%s) is trending", symbol, sym.Title),
		}

		select {
		case out <- sig:
			if f.maxStockTwitsPoll > 0 && newCount >= f.maxStockTwitsPoll {
				f.log.Info("stocktwits burst capped", "max_items", f.maxStockTwitsPoll)
				return
			}
		case <-ctx.Done():
			return
		}
	}

	if newCount > 0 {
		f.log.Info("stocktwits trending fetched", "new", newCount)
	}
}

// pollReddit fetches hot posts from a subreddit looking for ticker mentions
func (f *SocialFeed) pollReddit(ctx context.Context, out chan<- signal.Signal, subreddit string) {
	url := fmt.Sprintf("https://www.reddit.com/r/%s/hot.json?limit=25", subreddit)
	state := f.states["reddit/"+subreddit]
	if skip, remaining := state.ShouldPoll(time.Now()); skip {
		f.log.Debug("skipping reddit during backoff", "subreddit", subreddit, "retry_in", remaining)
		return
	}

	req, err := newFeedRequest(ctx, http.MethodGet, url)
	if err != nil {
		return
	}

	resp, err := f.client.Do(req)
	if err != nil {
		backoff := state.RecordFailure(time.Now(), f.interval)
		f.log.Warn("reddit fetch failed", "subreddit", subreddit, "error", err, "retry_after", backoff)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		backoff := state.RecordFailure(time.Now(), f.interval)
		f.log.Warn("reddit non-200", "subreddit", subreddit, "status", resp.StatusCode, "retry_after", backoff)
		return
	}
	state.RecordSuccess()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var listing struct {
		Data struct {
			Children []struct {
				Data struct {
					ID          string  `json:"id"`
					Title       string  `json:"title"`
					Selftext    string  `json:"selftext"`
					Score       int     `json:"score"`
					NumComments int     `json:"num_comments"`
					URL         string  `json:"url"`
					Ups         int     `json:"ups"`
					UpvoteRatio float64 `json:"upvote_ratio"`
					CreatedUTC  float64 `json:"created_utc"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return
	}

	newCount := 0
	for _, child := range listing.Data.Children {
		post := child.Data

		// Only process high-engagement posts
		if post.Score < 100 && post.NumComments < 50 {
			continue
		}

		id := fmt.Sprintf("reddit-%s-%s", subreddit, post.ID)
		if state.Seen(id) {
			continue
		}
		newCount++

		// Extract ticker mentions ($AAPL style)
		tickers := extractTickers(post.Title + " " + post.Selftext)
		entities := make([]signal.Entity, 0, len(tickers))
		for _, t := range tickers {
			entities = append(entities, signal.Entity{Name: t, Type: "instrument"})
		}

		urgency := socialUrgency(post.Score, post.NumComments)

		raw, _ := json.Marshal(map[string]any{
			"source":       "reddit",
			"subreddit":    subreddit,
			"title":        post.Title,
			"score":        post.Score,
			"num_comments": post.NumComments,
			"upvote_ratio": post.UpvoteRatio,
			"tickers":      tickers,
		})

		timestamp := time.Now()
		if post.CreatedUTC > 0 {
			timestamp = time.Unix(int64(post.CreatedUTC), 0).UTC()
		}

		sig := signal.Signal{
			ID:         id,
			Source:     fmt.Sprintf("reddit/%s", subreddit),
			Type:       signal.TypeSocial,
			Category:   "flows",
			Timestamp:  timestamp,
			Urgency:    urgency,
			Entities:   entities,
			Raw:        raw,
			Translated: fmt.Sprintf("Reddit r/%s: %s (score:%d comments:%d)", subreddit, post.Title, post.Score, post.NumComments),
		}

		select {
		case out <- sig:
			if f.maxRedditPoll > 0 && newCount >= f.maxRedditPoll {
				f.log.Info("reddit burst capped", "subreddit", subreddit, "max_items", f.maxRedditPoll)
				return
			}
		case <-ctx.Done():
			return
		}
	}

	if newCount > 0 {
		f.log.Info("reddit posts emitted", "subreddit", subreddit, "new", newCount)
	}
}

// extractTickers finds $TICKER patterns in text
func extractTickers(text string) []string {
	var tickers []string
	seen := make(map[string]bool)

	words := strings.Fields(text)
	for _, w := range words {
		w = strings.TrimRight(w, ".,!?;:)")
		if len(w) >= 2 && w[0] == '$' {
			ticker := strings.ToUpper(w[1:])
			// Basic validation: 1-5 uppercase letters
			if len(ticker) >= 1 && len(ticker) <= 5 && isAlpha(ticker) && !seen[ticker] {
				tickers = append(tickers, ticker)
				seen[ticker] = true
			}
		}
	}
	return tickers
}

func isAlpha(s string) bool {
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// socialUrgency maps engagement metrics to urgency
func socialUrgency(score, comments int) float64 {
	switch {
	case score > 5000 || comments > 1000:
		return 0.8
	case score > 1000 || comments > 300:
		return 0.6
	case score > 500 || comments > 100:
		return 0.5
	default:
		return 0.3
	}
}
