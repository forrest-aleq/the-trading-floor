package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

// EDGARFeed polls SEC EDGAR for recent corporate filings and emits them as signals.
// Uses the EDGAR full-text search RSS feed (no API key needed, 10 req/sec limit).
type EDGARFeed struct {
	log      *slog.Logger
	client   *http.Client
	interval time.Duration
	seen     map[string]bool
}

func NewEDGARFeed() *EDGARFeed {
	return &EDGARFeed{
		log: slog.Default().With("component", "feed-edgar"),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		interval: 5 * time.Minute,
		seen:     make(map[string]bool),
	}
}

func (f *EDGARFeed) Name() string { return "edgar" }

func (f *EDGARFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	// Initial fetch
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

const edgarRecentURL = "https://efts.sec.gov/LATEST/search-index?q=*&dateRange=custom&startdt=%s&enddt=%s&forms=10-K,10-Q,8-K,4,SC 13D,SC 13G,DEF 14A,S-1&from=0&size=20"

func (f *EDGARFeed) poll(ctx context.Context, out chan<- signal.Signal) {
	now := time.Now()
	start := now.Add(-6 * time.Hour).Format("2006-01-02")
	end := now.Format("2006-01-02")

	url := fmt.Sprintf(edgarRecentURL, start, end)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		f.log.Warn("edgar request build failed", "error", err)
		return
	}
	req.Header.Set("User-Agent", "TradingFloor/1.0 research@brechin.dev")
	req.Header.Set("Accept", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		f.log.Warn("edgar fetch failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		f.log.Warn("edgar non-200", "status", resp.StatusCode)
		return
	}

	var result edgarResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		f.log.Warn("edgar decode failed", "error", err)
		return
	}

	newCount := 0
	for _, hit := range result.Hits.Hits {
		src := hit.Source
		id := fmt.Sprintf("edgar-%s-%s", src.FileNum, src.FiledAt)

		if f.seen[id] {
			continue
		}
		f.seen[id] = true
		newCount++

		raw, _ := json.Marshal(map[string]any{
			"form_type":    src.FormType,
			"company":      src.EntityName,
			"ticker":       src.Tickers,
			"filed_at":     src.FiledAt,
			"file_num":     src.FileNum,
			"description":  src.DisplayNames,
		})

		urgency := filingUrgency(src.FormType)

		entities := make([]signal.Entity, 0, len(src.Tickers))
		for _, ticker := range src.Tickers {
			entities = append(entities, signal.Entity{Name: ticker, Type: "instrument"})
		}
		entities = append(entities, signal.Entity{Name: src.EntityName, Type: "company"})

		sig := signal.Signal{
			ID:        id,
			Source:    "sec-edgar",
			Type:      signal.TypeFiling,
			Category:  "corporate",
			Timestamp: time.Now(),
			Urgency:   urgency,
			Entities:  entities,
			Raw:       raw,
			Translated: fmt.Sprintf("SEC %s filing by %s (%v)", src.FormType, src.EntityName, src.Tickers),
		}

		select {
		case out <- sig:
		case <-ctx.Done():
			return
		}
	}

	if newCount > 0 {
		f.log.Info("edgar filings fetched", "new", newCount, "total_seen", len(f.seen))
	}

	// Prune old seen entries (keep last 1000)
	if len(f.seen) > 1000 {
		f.seen = make(map[string]bool)
	}
}

func filingUrgency(formType string) float64 {
	switch formType {
	case "8-K": // Current report — material events
		return 0.9
	case "4": // Insider trading
		return 0.8
	case "SC 13D", "SC 13G": // Large shareholder
		return 0.85
	case "S-1": // IPO registration
		return 0.7
	case "10-K", "10-Q": // Quarterly/annual financials
		return 0.6
	case "DEF 14A": // Proxy statement
		return 0.4
	default:
		return 0.3
	}
}

type edgarResponse struct {
	Hits struct {
		Hits []struct {
			Source edgarFiling `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type edgarFiling struct {
	FormType     string   `json:"form_type"`
	EntityName   string   `json:"entity_name"`
	Tickers      []string `json:"tickers"`
	FiledAt      string   `json:"file_date"`
	FileNum      string   `json:"file_num"`
	DisplayNames []string `json:"display_names"`
}
