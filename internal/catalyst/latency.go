package catalyst

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

type SourceSnapshot struct {
	Kind        string         `json:"kind"`
	ID          string         `json:"id,omitempty"`
	Fingerprint string         `json:"fingerprint"`
	State       map[string]any `json:"state,omitempty"`
	Error       string         `json:"error,omitempty"`
}

type MarketSnapshot struct {
	Ticker           string `json:"ticker"`
	Title            string `json:"title,omitempty"`
	Subtitle         string `json:"subtitle,omitempty"`
	Status           string `json:"status,omitempty"`
	YesBidDollars    string `json:"yes_bid_dollars,omitempty"`
	YesAskDollars    string `json:"yes_ask_dollars,omitempty"`
	NoBidDollars     string `json:"no_bid_dollars,omitempty"`
	NoAskDollars     string `json:"no_ask_dollars,omitempty"`
	LastPriceDollars string `json:"last_price_dollars,omitempty"`
	Fingerprint      string `json:"fingerprint"`
	Error            string `json:"error,omitempty"`
}

type Observation struct {
	RunID           string           `json:"run_id"`
	CatalystID      string           `json:"catalyst_id"`
	ObservedAt      time.Time        `json:"observed_at"`
	Event           string           `json:"event,omitempty"`
	Source          SourceSnapshot   `json:"source"`
	Markets         []MarketSnapshot `json:"markets"`
	SourceChangedAt *time.Time       `json:"source_changed_at,omitempty"`
	MarketChangedAt *time.Time       `json:"market_changed_at,omitempty"`
	LatencyMS       *int64           `json:"latency_ms,omitempty"`
}

type Detector struct {
	runID       string
	catalystID  string
	lastSource  string
	lastMarkets string

	sourceChangedAt *time.Time
	marketChangedAt *time.Time
	latencyMS       *int64
}

func NewDetector(runID, catalystID string) *Detector {
	return &Detector{
		runID:      strings.TrimSpace(runID),
		catalystID: strings.TrimSpace(catalystID),
	}
}

func (d *Detector) Observe(now time.Time, source SourceSnapshot, markets []MarketSnapshot) Observation {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	marketFingerprint := MarketSetFingerprint(markets)
	event := "snapshot"

	if d.lastSource != "" && source.Fingerprint != "" && source.Fingerprint != d.lastSource {
		changedAt := now
		d.sourceChangedAt = &changedAt
		d.marketChangedAt = nil
		d.latencyMS = nil
		event = "source_change"
	}
	if d.lastMarkets != "" && marketFingerprint != "" && marketFingerprint != d.lastMarkets {
		changedAt := now
		d.marketChangedAt = &changedAt
		if d.sourceChangedAt != nil && d.latencyMS == nil && !changedAt.Before(*d.sourceChangedAt) {
			latency := changedAt.Sub(*d.sourceChangedAt).Milliseconds()
			d.latencyMS = &latency
			event = "kalshi_reprice_after_source"
		} else if event == "snapshot" {
			event = "kalshi_change"
		}
	}

	if source.Fingerprint != "" {
		d.lastSource = source.Fingerprint
	}
	if marketFingerprint != "" {
		d.lastMarkets = marketFingerprint
	}

	return Observation{
		RunID:           d.runID,
		CatalystID:      d.catalystID,
		ObservedAt:      now,
		Event:           event,
		Source:          source,
		Markets:         markets,
		SourceChangedAt: d.sourceChangedAt,
		MarketChangedAt: d.marketChangedAt,
		LatencyMS:       d.latencyMS,
	}
}

func FingerprintJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func MarketSetFingerprint(markets []MarketSnapshot) string {
	if len(markets) == 0 {
		return ""
	}
	clean := append([]MarketSnapshot(nil), markets...)
	sort.Slice(clean, func(i, j int) bool {
		return clean[i].Ticker < clean[j].Ticker
	})
	for i := range clean {
		clean[i].Title = ""
		clean[i].Subtitle = ""
		clean[i].Error = ""
	}
	return FingerprintJSON(clean)
}
