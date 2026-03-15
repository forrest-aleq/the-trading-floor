package wire

import "github.com/hnic/trading-floor/pkg/signal"

// EnsureTranslatedText gives every signal a stable English-facing text field.
// Today this is best-effort extraction plus pass-through, which is still better
// than leaving downstream desks to reason over heterogeneous raw payloads.
func EnsureTranslatedText(sig signal.Signal) string {
	if sig.Translated != "" {
		return sig.Translated
	}
	return canonicalText(sig)
}
