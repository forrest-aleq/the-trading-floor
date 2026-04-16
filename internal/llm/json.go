package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractJSON finds and validates a JSON object in an LLM response.
// LLMs sometimes wrap JSON in markdown code fences or add prose around it.
func ExtractJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)

	// Try parsing directly
	if json.Valid([]byte(raw)) {
		return raw, nil
	}

	// Strip markdown code fences
	if idx := strings.Index(raw, "```json"); idx >= 0 {
		raw = raw[idx+7:]
		if end := strings.Index(raw, "```"); end >= 0 {
			raw = raw[:end]
		}
	} else if idx := strings.Index(raw, "```"); idx >= 0 {
		raw = raw[idx+3:]
		if end := strings.Index(raw, "```"); end >= 0 {
			raw = raw[:end]
		}
	}
	raw = strings.TrimSpace(raw)

	if json.Valid([]byte(raw)) {
		return raw, nil
	}

	if candidate, ok := findJSONCandidate(raw); ok {
		return candidate, nil
	}

	// Try to find first { to last }
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		candidate := raw[start : end+1]
		if json.Valid([]byte(candidate)) {
			return candidate, nil
		}
	}
	start = strings.Index(raw, "[")
	end = strings.LastIndex(raw, "]")
	if start >= 0 && end > start {
		candidate := raw[start : end+1]
		if json.Valid([]byte(candidate)) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no valid JSON found in response (len=%d)", len(raw))
}

func findJSONCandidate(raw string) (string, bool) {
	best := jsonCandidate{}
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '{', '[':
		default:
			continue
		}
		candidate, ok := decodeJSONPrefix(raw[i:])
		if !ok {
			continue
		}
		entry := jsonCandidate{
			raw:   candidate,
			start: i,
			isObj: strings.HasPrefix(candidate, "{"),
		}
		if best.betterThan(entry) {
			continue
		}
		best = entry
	}
	if best.raw == "" {
		return "", false
	}
	return best.raw, true
}

type jsonCandidate struct {
	raw   string
	start int
	isObj bool
}

func (c jsonCandidate) betterThan(other jsonCandidate) bool {
	if c.raw == "" {
		return false
	}
	if c.isObj != other.isObj {
		return c.isObj
	}
	if len(c.raw) != len(other.raw) {
		return len(c.raw) > len(other.raw)
	}
	return c.start > other.start
}

func decodeJSONPrefix(raw string) (string, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}
	offset := int(decoder.InputOffset())
	if offset <= 0 || offset > len(raw) {
		return "", false
	}
	candidate := strings.TrimSpace(raw[:offset])
	if !json.Valid([]byte(candidate)) {
		return "", false
	}
	return candidate, true
}

// ValidateJSONFields checks that required top-level keys exist in a JSON string.
func ValidateJSONFields(raw string, required []string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	var missing []string
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}

	return nil
}
