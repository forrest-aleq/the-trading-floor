package wire

import (
	"encoding/json"
	"strings"

	"github.com/hnic/trading-floor/pkg/signal"
)

// NormalizeSignal prepares raw signals for downstream reasoning. It canonicalizes
// metadata, extracts a display text, and ensures embeddings/content hashes exist.
func NormalizeSignal(sig signal.Signal) signal.Signal {
	sig.Source = strings.TrimSpace(strings.ToLower(sig.Source))
	sig.Category = strings.TrimSpace(strings.ToLower(sig.Category))
	if sig.Strength == 0 {
		sig.Strength = sig.Urgency
	}
	if len(sig.Languages) == 0 {
		sig.Languages = []string{"en"}
	}
	if sig.Translated == "" {
		sig.Translated = EnsureTranslatedText(sig)
	}
	if sig.ContentHash == "" {
		sig.ContentHash = hashSignalContent(sig)
	}
	if len(sig.Embedding) == 0 {
		sig.Embedding = EmbedText(sig.Translated)
	}
	sig.EvidenceMeta = buildEvidenceMeta(sig)
	return sig
}

func canonicalText(sig signal.Signal) string {
	if sig.Translated != "" {
		return strings.TrimSpace(sig.Translated)
	}

	if len(sig.Raw) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(sig.Raw, &text); err == nil {
		return strings.TrimSpace(text)
	}

	var obj map[string]any
	if err := json.Unmarshal(sig.Raw, &obj); err == nil {
		parts := make([]string, 0, 4)
		for _, key := range []string{"title", "headline", "description", "summary", "content", "text", "symbol"} {
			if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
				parts = append(parts, strings.TrimSpace(value))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " | ")
		}
	}

	return strings.TrimSpace(string(sig.Raw))
}
