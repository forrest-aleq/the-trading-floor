package kalshi

import (
	"regexp"
	"sort"
	"strings"
)

const (
	MVEClassSingleLeg  = "single_leg"
	MVEClassSameMatch  = "same_match"
	MVEClassCrossMatch = "cross_match"
	MVEClassMixedMatch = "mixed_match"
	MVEClassUnknown    = "unknown"
)

var mveTickerDatePattern = regexp.MustCompile(`[0-9]{2}[A-Z]{3}[0-9]{2}`)

type MVEFairValueClassification struct {
	Class           string   `json:"class"`
	UniqueEventKeys int      `json:"unique_event_keys"`
	EventKeys       []string `json:"event_keys,omitempty"`
}

func canonicalMVEEventKey(leg MVESelectedLeg, market Market) string {
	for _, text := range []string{market.Title, market.Subtitle} {
		if left, right, ok := parseMatchTeams(text); ok {
			teams := []string{normalizeMVEKeyPart(left), normalizeMVEKeyPart(right)}
			sort.Strings(teams)
			date := firstMVETickerDate(leg.EventTicker, market.EventTicker, leg.MarketTicker, market.Ticker)
			key := "match:" + strings.Join(teams, ":")
			if date != "" {
				key += ":" + strings.ToLower(date)
			}
			return key
		}
	}
	if eventTicker := normalizeMVEKeyPart(firstNonEmpty(leg.EventTicker, market.EventTicker)); eventTicker != "" {
		return "event:" + eventTicker
	}
	if ticker := normalizeMVEKeyPart(firstNonEmpty(leg.MarketTicker, market.Ticker)); ticker != "" {
		return "market:" + ticker
	}
	return ""
}

func classifyMVEFairValueLegs(legs []MVEFairValueLeg) *MVEFairValueClassification {
	if len(legs) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, leg := range legs {
		key := strings.TrimSpace(leg.EventKey)
		if key == "" {
			key = normalizeMVEKeyPart(firstNonEmpty(leg.EventTicker, leg.MarketTicker))
		}
		if key == "" {
			key = MVEClassUnknown
		}
		counts[key]++
	}
	keys := make([]string, 0, len(counts))
	repeated := false
	for key, count := range counts {
		keys = append(keys, key)
		if count > 1 {
			repeated = true
		}
	}
	sort.Strings(keys)

	class := MVEClassCrossMatch
	switch {
	case len(legs) == 1:
		class = MVEClassSingleLeg
	case len(keys) == 1:
		class = MVEClassSameMatch
	case repeated:
		class = MVEClassMixedMatch
	}
	return &MVEFairValueClassification{
		Class:           class,
		UniqueEventKeys: len(keys),
		EventKeys:       keys,
	}
}

func parseMatchTeams(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", false
	}
	if idx := strings.Index(text, ":"); idx > 0 {
		text = text[:idx]
	}
	lower := strings.ToLower(text)
	for _, sep := range []string{" vs. ", " vs ", " v. ", " v ", " at ", " @ "} {
		if idx := strings.Index(lower, sep); idx > 0 {
			left := strings.TrimSpace(text[:idx])
			right := strings.TrimSpace(text[idx+len(sep):])
			if left != "" && right != "" {
				return left, right, true
			}
		}
	}
	return "", "", false
}

func firstMVETickerDate(values ...string) string {
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if match := mveTickerDatePattern.FindString(value); match != "" {
			return match
		}
	}
	return ""
}

func normalizeMVEKeyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
