package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/execution/kalshi"
	"github.com/hnic/trading-floor/pkg/signal"
)

type KalshiFeed struct {
	log                *slog.Logger
	client             kalshiMarketClient
	sportsAvailability SportsAvailabilityProvider
	interval           time.Duration
	limit              int
	maxPages           int
	state              *sourceState
}

type kalshiMarketClient interface {
	GetMarkets(ctx context.Context, status string, limit int, cursor string) (*kalshi.MarketsResponse, error)
}

type SportsAvailabilityProvider interface {
	CheckMarket(ctx context.Context, market kalshi.Market, now time.Time) SportsAvailabilityEvidence
}

type SportsAvailabilityEvidence struct {
	Status     string     `json:"status,omitempty"`
	Source     string     `json:"source,omitempty"`
	League     string     `json:"league,omitempty"`
	EventID    string     `json:"event_id,omitempty"`
	EventName  string     `json:"event_name,omitempty"`
	Player     string     `json:"player,omitempty"`
	Team       string     `json:"team,omitempty"`
	Active     *bool      `json:"active,omitempty"`
	Starter    *bool      `json:"starter,omitempty"`
	Position   string     `json:"position,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

func NewKalshiFeed(client kalshiMarketClient) *KalshiFeed {
	return &KalshiFeed{
		log:      slog.Default().With("component", "feed-kalshi"),
		client:   client,
		interval: readFeedDuration("KALSHI_FEED_INTERVAL", 2*time.Minute),
		limit:    readFeedInt("KALSHI_FEED_MARKET_LIMIT", 100),
		maxPages: readFeedInt("KALSHI_FEED_MAX_PAGES", 10),
		state:    newSourceState(4096),
	}
}

func (f *KalshiFeed) Name() string { return "kalshi" }

func (f *KalshiFeed) SetSportsAvailabilityProvider(provider SportsAvailabilityProvider) *KalshiFeed {
	if f != nil {
		f.sportsAvailability = provider
	}
	return f
}

func (f *KalshiFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	if f.client == nil {
		f.log.Info("kalshi feed disabled; no client configured")
		<-ctx.Done()
		return ctx.Err()
	}

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	f.fetchAndSend(ctx, out)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			f.fetchAndSend(ctx, out)
		}
	}
}

func (f *KalshiFeed) fetchAndSend(ctx context.Context, out chan<- signal.Signal) {
	if skip, remaining := f.state.ShouldPoll(time.Now()); skip {
		f.log.Debug("skipping kalshi feed during backoff", "retry_in", remaining)
		return
	}

	markets, pages, cursor, err := f.fetchOpenMarkets(ctx)
	if err != nil {
		backoff := f.state.RecordFailure(time.Now(), f.interval)
		f.log.Warn("kalshi market fetch failed", "error", err, "retry_after", backoff)
		return
	}
	f.state.RecordSuccess()
	if strings.TrimSpace(cursor) != "" {
		f.log.Warn("kalshi market pagination capped",
			"pages", pages,
			"limit", f.limit,
			"max_pages", f.maxPages,
			"next_cursor", cursor,
		)
	}

	now := time.Now().UTC()
	emitted := 0
	for _, market := range markets {
		if strings.TrimSpace(market.Ticker) == "" {
			continue
		}
		if kalshi.ShouldBlockMultivariateTicker(market.Ticker) {
			f.log.Debug("kalshi MVE wrapper skipped", "ticker", market.Ticker)
			continue
		}
		if !kalshiMarketHasActionablePrice(market) {
			continue
		}
		id := fmt.Sprintf("kalshi-%s-%s", market.Ticker, kalshiMarketSnapshotKey(market))
		if f.state.Seen(id) {
			continue
		}
		availability := SportsAvailabilityEvidence{}
		if f.sportsAvailability != nil {
			availability = f.sportsAvailability.CheckMarket(ctx, market, now)
		}
		raw, err := marshalKalshiMarketSignalRaw(market, availability)
		if err != nil {
			f.log.Warn("kalshi market marshal failed", "ticker", market.Ticker, "error", err)
			continue
		}
		text := formatKalshiMarketSignalText(market)
		if availabilityText := availability.EvidenceLine(); availabilityText != "" {
			text += " | " + availabilityText
		}
		entities := []signal.Entity{
			{Name: market.Ticker, Type: "prediction_market", ID: market.Ticker},
		}
		if strings.TrimSpace(availability.Player) != "" {
			entities = append(entities, signal.Entity{Name: availability.Player, Type: "person"})
		}
		if strings.TrimSpace(availability.Team) != "" {
			entities = append(entities, signal.Entity{Name: availability.Team, Type: "team"})
		}
		out <- signal.Signal{
			ID:         id,
			Source:     "kalshi-market",
			Type:       signal.TypeAlternative,
			Category:   "prediction_market",
			Timestamp:  now,
			Urgency:    kalshiMarketUrgency(market),
			Strength:   0.45,
			Raw:        raw,
			Translated: text,
			Entities:   entities,
		}
		emitted++
	}
	if emitted > 0 {
		f.log.Info("kalshi market signals emitted", "count", emitted, "markets", len(markets), "pages", pages)
	}
}

func (f *KalshiFeed) fetchOpenMarkets(ctx context.Context) ([]kalshi.Market, int, string, error) {
	maxPages := f.maxPages
	if maxPages <= 0 {
		maxPages = 1
	}
	limit := f.limit
	if limit <= 0 {
		limit = 100
	}

	var markets []kalshi.Market
	cursor := ""
	pages := 0
	for pages < maxPages {
		resp, err := f.client.GetMarkets(ctx, "open", limit, cursor)
		if err != nil {
			return nil, pages, cursor, err
		}
		pages++
		if resp != nil {
			markets = append(markets, resp.Markets...)
			cursor = strings.TrimSpace(resp.Cursor)
		} else {
			cursor = ""
		}
		if cursor == "" {
			break
		}
	}
	return markets, pages, cursor, nil
}

func kalshiMarketHasActionablePrice(market kalshi.Market) bool {
	for _, raw := range []string{
		market.YesBidDollars,
		market.YesAskDollars,
		market.NoBidDollars,
		market.NoAskDollars,
		market.LastPriceDollars,
	} {
		price, ok := parsePrice(raw)
		if ok && price > 0 && price < 1 {
			return true
		}
	}
	return false
}

func marshalKalshiMarketSignalRaw(market kalshi.Market, availability SportsAvailabilityEvidence) ([]byte, error) {
	raw, err := json.Marshal(market)
	if err != nil {
		return nil, err
	}
	if availability.Status == "" {
		return raw, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	payload["sports_availability"] = availability
	return json.Marshal(payload)
}

func NewKalshiClientFromEnv() *kalshi.Client {
	if !readKalshiFeedEnabled() {
		return nil
	}
	client, err := kalshi.NewClient(kalshi.DefaultConfig())
	if err != nil {
		slog.Default().With("component", "feed-kalshi").Warn("kalshi client init failed", "error", err)
		return nil
	}
	return client
}

func readKalshiFeedEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("KALSHI_FEED_ENABLED"))
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return parsed
}

func kalshiMarketUrgency(market kalshi.Market) float64 {
	bid, bidOK := parsePrice(market.YesBidDollars)
	ask, askOK := parsePrice(market.YesAskDollars)
	if bidOK && askOK && ask > bid {
		spread := ask - bid
		switch {
		case spread <= 0.03:
			return 0.65
		case spread <= 0.08:
			return 0.5
		}
	}
	return 0.35
}

func parsePrice(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func formatKalshiMarketSignalText(market kalshi.Market) string {
	parts := []string{
		"Kalshi market",
		strings.TrimSpace(market.Ticker),
		compactFeedText(market.Title, 180),
		compactFeedText(market.Subtitle, 120),
	}
	priceParts := []string{}
	if market.YesBidDollars != "" {
		priceParts = append(priceParts, "yes_bid="+market.YesBidDollars)
	}
	if market.YesAskDollars != "" {
		priceParts = append(priceParts, "yes_ask="+market.YesAskDollars)
	}
	if market.NoBidDollars != "" {
		priceParts = append(priceParts, "no_bid="+market.NoBidDollars)
	}
	if market.NoAskDollars != "" {
		priceParts = append(priceParts, "no_ask="+market.NoAskDollars)
	}
	if market.LastPriceDollars != "" {
		priceParts = append(priceParts, "last="+market.LastPriceDollars)
	}
	if len(priceParts) > 0 {
		parts = append(parts, strings.Join(priceParts, " "))
	}
	if market.CloseTime != "" {
		parts = append(parts, "close="+market.CloseTime)
	}
	if market.ExpirationTime != "" {
		parts = append(parts, "expiration="+market.ExpirationTime)
	}

	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return strings.TrimSpace(market.Ticker)
	}
	return strings.Join(cleaned, " | ")
}

func (e SportsAvailabilityEvidence) EvidenceLine() string {
	status := strings.ToLower(strings.TrimSpace(e.Status))
	if status == "" {
		return ""
	}
	parts := []string{"participant_availability: " + status}
	appendPart := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	appendPart("source", e.Source)
	appendPart("league", e.League)
	appendPart("event", e.EventID)
	appendPart("player", e.Player)
	appendPart("team", e.Team)
	if e.Active != nil {
		appendPart("active", strconv.FormatBool(*e.Active))
	}
	if e.Starter != nil {
		appendPart("starter", strconv.FormatBool(*e.Starter))
	}
	appendPart("position", e.Position)
	appendPart("reason", e.Reason)
	if e.ObservedAt != nil && !e.ObservedAt.IsZero() {
		appendPart("observed_at", e.ObservedAt.UTC().Format(time.RFC3339))
	}
	return strings.Join(parts, " ")
}

func kalshiMarketSnapshotKey(market kalshi.Market) string {
	values := []string{
		strings.TrimSpace(market.Status),
		strings.TrimSpace(market.YesBidDollars),
		strings.TrimSpace(market.YesAskDollars),
		strings.TrimSpace(market.NoBidDollars),
		strings.TrimSpace(market.NoAskDollars),
		strings.TrimSpace(market.LastPriceDollars),
		strings.TrimSpace(market.CloseTime),
		strings.TrimSpace(market.ExpirationTime),
	}
	joined := strings.Join(values, "|")
	if strings.Trim(joined, "|") == "" {
		return "initial"
	}
	return strings.NewReplacer(".", "p", ":", "", "-", "", " ", "").Replace(joined)
}

func compactFeedText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
