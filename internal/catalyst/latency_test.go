package catalyst

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDetectorMeasuresFirstKalshiMoveAfterSourceChange(t *testing.T) {
	detector := NewDetector("run-1", "lineup-rsa-can")
	start := time.Date(2026, 6, 28, 17, 59, 58, 0, time.UTC)

	sourcePre := SourceSnapshot{Kind: "espn_lineup", ID: "760475", Fingerprint: FingerprintJSON(map[string]any{"lineups": false})}
	sourcePost := SourceSnapshot{Kind: "espn_lineup", ID: "760475", Fingerprint: FingerprintJSON(map[string]any{"lineups": true})}
	marketPre := []MarketSnapshot{{Ticker: "KXGOAL-DAVID", YesBidDollars: "0.3200", YesAskDollars: "0.3400", Fingerprint: "mkt-pre"}}
	marketPost := []MarketSnapshot{{Ticker: "KXGOAL-DAVID", YesBidDollars: "0.0800", YesAskDollars: "0.1000", Fingerprint: "mkt-post"}}

	first := detector.Observe(start, sourcePre, marketPre)
	if first.Event != "snapshot" || first.LatencyMS != nil {
		t.Fatalf("unexpected first observation: %+v", first)
	}
	sourceChanged := detector.Observe(start.Add(2*time.Second), sourcePost, marketPre)
	if sourceChanged.Event != "source_change" || sourceChanged.SourceChangedAt == nil {
		t.Fatalf("expected source change: %+v", sourceChanged)
	}
	repriced := detector.Observe(start.Add(7*time.Second), sourcePost, marketPost)
	if repriced.Event != "kalshi_reprice_after_source" {
		t.Fatalf("expected Kalshi reprice event, got %+v", repriced)
	}
	if repriced.LatencyMS == nil || *repriced.LatencyMS != 5000 {
		t.Fatalf("latency = %v, want 5000ms", repriced.LatencyMS)
	}
}

func TestObservationJSONContainsLatencyFields(t *testing.T) {
	latency := int64(1234)
	now := time.Date(2026, 6, 28, 18, 0, 1, 0, time.UTC)
	obs := Observation{
		RunID:      "run-1",
		CatalystID: "spotify-chart",
		ObservedAt: now,
		Event:      "kalshi_reprice_after_source",
		Source: SourceSnapshot{
			Kind:        "generic_json",
			Fingerprint: "abc",
		},
		Markets: []MarketSnapshot{{
			Ticker:      "KXSPOTIFY-TEST",
			Fingerprint: "def",
		}},
		SourceChangedAt: &now,
		MarketChangedAt: &now,
		LatencyMS:       &latency,
	}
	raw, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["latency_ms"].(float64) != 1234 {
		t.Fatalf("unexpected JSON: %s", raw)
	}
	if decoded["event"] != "kalshi_reprice_after_source" {
		t.Fatalf("unexpected JSON: %s", raw)
	}
}
