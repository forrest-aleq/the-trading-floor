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

	// Try to find first { to last }
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		candidate := raw[start : end+1]
		if json.Valid([]byte(candidate)) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no valid JSON found in response (len=%d)", len(raw))
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
