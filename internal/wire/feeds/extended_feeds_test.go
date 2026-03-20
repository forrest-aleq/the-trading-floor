package feeds

import (
	"testing"
)

func TestDefaultTelegramSourcesFromEnv(t *testing.T) {
	t.Setenv("TELEGRAM_FEED_URLS", "https://example.com/channel-a.rss, https://example.com/channel-b.rss")
	t.Setenv("TELEGRAM_FEED_NAMES", "mena-wire, sanctions-watch")
	t.Setenv("TELEGRAM_FEED_CATEGORY", "geopolitical")
	t.Setenv("TELEGRAM_FEED_LANGUAGE", "ar")
	t.Setenv("TELEGRAM_FEED_INTERVAL_SECONDS", "45")

	sources := defaultTelegramSourcesFromEnv()
	if len(sources) != 2 {
		t.Fatalf("expected 2 telegram sources, got %d", len(sources))
	}
	if sources[0].Name != "mena-wire" || sources[1].Name != "sanctions-watch" {
		t.Fatalf("unexpected source names: %+v", sources)
	}
	if sources[0].Category != "geopolitical" || sources[0].Language != "ar" {
		t.Fatalf("unexpected source metadata: %+v", sources[0])
	}
}

func TestParseEarningsEvents(t *testing.T) {
	body := []byte(`[
		{
			"symbol": "AAPL",
			"name": "Apple Inc.",
			"date": "2026-03-20",
			"time": "amc",
			"eps": 2.20,
			"epsEstimated": 2.05,
			"revenue": 123000000000,
			"revenueEstimated": 120000000000
		}
	]`)

	events, err := parseEarningsEvents(body)
	if err != nil {
		t.Fatalf("parseEarningsEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Symbol != "AAPL" || events[0].Time != "amc" {
		t.Fatalf("unexpected earnings event: %+v", events[0])
	}
}

func TestParseAlternativeItems(t *testing.T) {
	body := []byte(`{
		"items": [
			{
				"id": "job-1",
				"title": "NVIDIA hiring supply chain operations lead",
				"description": "Expansion in Taiwan operations",
				"url": "https://example.com/jobs/1",
				"symbol": "NVDA",
				"published_at": "2026-03-13T12:00:00Z"
			}
		]
	}`)

	items, err := parseAlternativeItems(body)
	if err != nil {
		t.Fatalf("parseAlternativeItems failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 alternative item, got %d", len(items))
	}
	if items[0].Symbol != "NVDA" || items[0].ID != "job-1" {
		t.Fatalf("unexpected alternative item: %+v", items[0])
	}
}

func TestDefaultNewsSourcesPreferLiveNonDuplicateFeeds(t *testing.T) {
	sources := DefaultNewsSources()
	if len(sources) < 4 {
		t.Fatalf("expected hardened news defaults, got %d sources", len(sources))
	}

	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if _, ok := seen[source.Name]; ok {
			t.Fatalf("duplicate source %q", source.Name)
		}
		seen[source.Name] = struct{}{}
	}

	for _, legacy := range []string{"reuters-business", "reuters-markets", "wsj-markets", "cnbc-top", "sec-edgar"} {
		if _, ok := seen[legacy]; ok {
			t.Fatalf("legacy dead or duplicated source %q still present", legacy)
		}
	}

	for _, required := range []string{"ft-markets", "ft-companies", "fed-press", "fed-speeches"} {
		if _, ok := seen[required]; !ok {
			t.Fatalf("missing required hardened source %q", required)
		}
	}
}

func TestExtraNewsSourcesFromEnv(t *testing.T) {
	t.Setenv("WIRE_NEWS_EXTRA_SOURCES_JSON", `[
		{"name":"aljazeera-ar","url":"https://example.com/aljazeera-ar.rss","category":"geopolitical","language":"ar","interval_seconds":75},
		{"name":"asia-brief-zh","url":"https://example.com/asia-zh.rss","category":"macro","language":"zh"}
	]`)

	sources := extraNewsSourcesFromEnv()
	if len(sources) != 2 {
		t.Fatalf("expected 2 extra news sources, got %d", len(sources))
	}
	if sources[0].Language != "ar" || sources[0].Interval.Seconds() != 75 {
		t.Fatalf("unexpected first source metadata: %+v", sources[0])
	}
	if sources[1].Language != "zh" || sources[1].Interval <= 0 {
		t.Fatalf("unexpected second source metadata: %+v", sources[1])
	}
}
