package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

// MacroFeed monitors macroeconomic data releases from FRED (Federal Reserve Economic Data)
// and the economic calendar. Emits signals for major economic releases.
type MacroFeed struct {
	log      *slog.Logger
	client   *http.Client
	apiKey   string // FRED API key (free: https://fred.stlouisfed.org/docs/api/api_key.html)
	interval time.Duration
	seen     map[string]bool
	series   []macroSeries
}

type macroSeries struct {
	ID       string  // FRED series ID
	Name     string  // Human-readable name
	Category string  // Signal category
	Urgency  float64 // Base urgency
}

func defaultMacroSeries() []macroSeries {
	return []macroSeries{
		{"UNRATE", "Unemployment Rate", "macro", 0.8},
		{"CPIAUCSL", "CPI (All Urban Consumers)", "macro", 0.9},
		{"FEDFUNDS", "Federal Funds Rate", "macro", 0.95},
		{"GDP", "Gross Domestic Product", "macro", 0.85},
		{"PAYEMS", "Nonfarm Payrolls", "macro", 0.9},
		{"UMCSENT", "Consumer Sentiment (UMich)", "macro", 0.6},
		{"T10Y2Y", "10Y-2Y Treasury Spread", "macro", 0.7},
		{"VIXCLS", "VIX Close", "macro", 0.5},
		{"DGS10", "10-Year Treasury Rate", "macro", 0.7},
		{"DTWEXBGS", "Trade-Weighted Dollar Index", "macro", 0.6},
		{"BAMLH0A0HYM2", "High Yield OAS", "macro", 0.7},
		{"ICSA", "Initial Jobless Claims", "macro", 0.75},
	}
}

func NewMacroFeed(fredAPIKey string) *MacroFeed {
	return &MacroFeed{
		log:      slog.Default().With("component", "feed-macro"),
		client:   newFeedHTTPClient(),
		apiKey:   fredAPIKey,
		interval: 15 * time.Minute,
		seen:     make(map[string]bool),
		series:   defaultMacroSeries(),
	}
}

func (f *MacroFeed) Name() string { return "macro" }

func (f *MacroFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	if f.apiKey == "" {
		f.log.Warn("FRED API key not set — macro feed disabled (set FRED_API_KEY)")
		<-ctx.Done()
		return ctx.Err()
	}

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

func (f *MacroFeed) poll(ctx context.Context, out chan<- signal.Signal) {
	for _, s := range f.series {
		select {
		case <-ctx.Done():
			return
		default:
		}
		f.fetchSeries(ctx, s, out)
		// Rate limit: FRED allows 120 req/min
		time.Sleep(500 * time.Millisecond)
	}
}

func (f *MacroFeed) fetchSeries(ctx context.Context, s macroSeries, out chan<- signal.Signal) {
	url := fmt.Sprintf(
		"https://api.stlouisfed.org/fred/series/observations?series_id=%s&api_key=%s&file_type=json&sort_order=desc&limit=2",
		s.ID, f.apiKey,
	)

	req, err := newFeedRequest(ctx, http.MethodGet, url)
	if err != nil {
		return
	}

	resp, err := f.client.Do(req)
	if err != nil {
		f.log.Warn("fred fetch failed", "series", s.ID, "error", err)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var result fredResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return
	}

	if len(result.Observations) == 0 {
		return
	}

	latest := result.Observations[0]
	id := fmt.Sprintf("fred-%s-%s", s.ID, latest.Date)

	if f.seen[id] {
		return
	}
	f.seen[id] = true

	// Calculate change if we have previous observation
	var changePct float64
	var previous string
	if len(result.Observations) > 1 {
		prev := result.Observations[1]
		previous = prev.Value
		// Parse values for change calculation
		var cur, prv float64
		if _, err := fmt.Sscanf(latest.Value, "%f", &cur); err == nil {
			if _, err := fmt.Sscanf(prev.Value, "%f", &prv); err == nil && prv != 0 {
				changePct = (cur - prv) / prv * 100
			}
		}
	}

	// Boost urgency for large changes
	urgency := s.Urgency
	if changePct > 5 || changePct < -5 {
		urgency = min(1.0, urgency+0.2)
	}

	raw, _ := json.Marshal(map[string]any{
		"series_id":      s.ID,
		"series_name":    s.Name,
		"date":           latest.Date,
		"value":          latest.Value,
		"previous_value": previous,
		"change_pct":     changePct,
	})

	timestamp := time.Now()
	if parsed, ok := parsePublishedTime(latest.Date); ok {
		timestamp = parsed.UTC()
	}

	sig := signal.Signal{
		ID:        id,
		Source:    "fred",
		Type:      signal.TypeEconomic,
		Category:  s.Category,
		Timestamp: timestamp,
		Urgency:   urgency,
		Raw:       raw,
		Translated: fmt.Sprintf("FRED %s: %s = %s (date: %s, change: %.2f%%)",
			s.ID, s.Name, latest.Value, latest.Date, changePct),
	}

	select {
	case out <- sig:
		f.log.Info("macro data point", "series", s.ID, "value", latest.Value, "date", latest.Date)
	case <-ctx.Done():
	}

	// Prune old seen entries
	if len(f.seen) > 500 {
		f.seen = make(map[string]bool)
	}
}

type fredResponse struct {
	Observations []fredObservation `json:"observations"`
}

type fredObservation struct {
	Date  string `json:"date"`
	Value string `json:"value"`
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
