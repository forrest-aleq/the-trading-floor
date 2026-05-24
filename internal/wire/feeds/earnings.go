package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

const earningsCalendarURL = "https://financialmodelingprep.com/stable/earnings-calendar?from=%s&to=%s&apikey=%s"

type EarningsFeed struct {
	log      *slog.Logger
	client   *http.Client
	apiKey   string
	interval time.Duration
	seen     map[string]bool
	watch    map[string]struct{}
}

type earningsEvent struct {
	Symbol             string  `json:"symbol"`
	Name               string  `json:"name"`
	Date               string  `json:"date"`
	Time               string  `json:"time"`
	EPS                float64 `json:"eps"`
	EPSEstimated       float64 `json:"epsEstimated"`
	Revenue            float64 `json:"revenue"`
	RevenueEstimated   float64 `json:"revenueEstimated"`
	UpdatedFromPayload string  `json:"updatedFromPayload,omitempty"`
}

func NewEarningsFeed(apiKey string, instruments []model.Instrument) *EarningsFeed {
	apiKey = resolveFMPAPIKey(apiKey)

	watch := make(map[string]struct{})
	for _, inst := range instruments {
		if inst.SecType != "STK" || strings.TrimSpace(inst.Symbol) == "" {
			continue
		}
		watch[strings.ToUpper(inst.Symbol)] = struct{}{}
	}

	return &EarningsFeed{
		log:      slog.Default().With("component", "feed-earnings"),
		client:   newFeedHTTPClient(),
		apiKey:   apiKey,
		interval: parseIntervalSecondsEnv("EARNINGS_FEED_INTERVAL_SECONDS", 3600),
		seen:     make(map[string]bool),
		watch:    watch,
	}
}

func (f *EarningsFeed) Name() string { return "earnings" }

func (f *EarningsFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	if f.apiKey == "" {
		f.log.Warn("earnings feed disabled — set FMP_API_KEY (legacy alias: EARNINGS_API_KEY)")
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

func (f *EarningsFeed) poll(ctx context.Context, out chan<- signal.Signal) {
	now := time.Now().UTC()
	from := now.Format("2006-01-02")
	to := now.Add(14 * 24 * time.Hour).Format("2006-01-02")
	endpoint := fmt.Sprintf(earningsCalendarURL, from, to, f.apiKey)

	req, err := newFeedRequest(ctx, http.MethodGet, endpoint)
	if err != nil {
		return
	}

	resp, err := f.client.Do(req)
	if err != nil {
		f.log.Warn("earnings fetch failed", "error", err)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		f.log.Warn("earnings non-200", "status", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	events, err := parseEarningsEvents(body)
	if err != nil {
		f.log.Warn("earnings decode failed", "error", err)
		return
	}

	sort.Slice(events, func(i, j int) bool {
		if events[i].Date == events[j].Date {
			return events[i].Symbol < events[j].Symbol
		}
		return events[i].Date < events[j].Date
	})

	for _, event := range events {
		symbol := strings.ToUpper(strings.TrimSpace(event.Symbol))
		if symbol == "" {
			continue
		}
		if len(f.watch) > 0 {
			if _, ok := f.watch[symbol]; !ok {
				continue
			}
		}

		id := fmt.Sprintf("earnings-%s-%s-%s", symbol, event.Date, event.Time)
		if f.seen[id] {
			continue
		}
		f.seen[id] = true

		raw, _ := json.Marshal(event)
		sig := signal.Signal{
			ID:        id,
			Source:    "earnings-calendar",
			Type:      signal.TypeNews,
			Category:  "corporate",
			Timestamp: time.Now(),
			Urgency:   earningsUrgency(event),
			Entities: []signal.Entity{
				{Name: symbol, Type: "instrument"},
			},
			Raw: raw,
			Translated: fmt.Sprintf(
				"Earnings calendar: %s scheduled %s on %s (EPS est %.2f)",
				symbol,
				defaultString(event.Time, "unspecified"),
				event.Date,
				event.EPSEstimated,
			),
		}

		select {
		case out <- sig:
		case <-ctx.Done():
			return
		}
	}
}

func parseEarningsEvents(body []byte) ([]earningsEvent, error) {
	var events []earningsEvent
	if err := json.Unmarshal(body, &events); err == nil && len(events) > 0 {
		return events, nil
	}

	var wrapped struct {
		Data []earningsEvent `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return wrapped.Data, nil
	}

	return nil, fmt.Errorf("unexpected earnings response shape")
}

func earningsUrgency(event earningsEvent) float64 {
	base := 0.7
	if strings.EqualFold(event.Time, "amc") || strings.EqualFold(event.Time, "bmo") {
		base += 0.1
	}
	if event.EPSEstimated != 0 && event.EPS != 0 {
		surprise := abs(event.EPS-event.EPSEstimated) / abs(event.EPSEstimated)
		if surprise > 0.1 {
			base += 0.15
		}
	}
	if base > 0.95 {
		return 0.95
	}
	return base
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func resolveFMPAPIKey(explicit string) string {
	if key := strings.TrimSpace(explicit); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("FMP_API_KEY")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv("EARNINGS_API_KEY"))
}
