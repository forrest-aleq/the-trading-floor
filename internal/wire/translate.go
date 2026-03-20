package wire

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/hnic/trading-floor/internal/sanitize"
	"github.com/hnic/trading-floor/pkg/signal"
)

type TranslationResult struct {
	OriginalText     string
	TranslatedText   string
	OriginalLanguage string
	Provider         string
	Confidence       float64
}

type Translator interface {
	Translate(sig signal.Signal) TranslationResult
}

var (
	defaultTranslatorOnce sync.Once
	defaultTranslator     Translator
)

func DefaultTranslator() Translator {
	defaultTranslatorOnce.Do(func() {
		switch strings.TrimSpace(strings.ToLower(os.Getenv("WIRE_TRANSLATOR"))) {
		case "none", "pass", "passthrough", "":
			defaultTranslator = passThroughTranslator{}
		default:
			defaultTranslator = passThroughTranslator{}
		}
	})
	return defaultTranslator
}

func applyTranslation(sig signal.Signal) signal.Signal {
	result := DefaultTranslator().Translate(sig)
	if result.OriginalText != "" {
		sig.OriginalText = result.OriginalText
	}
	if result.TranslatedText != "" {
		sig.Translated = result.TranslatedText
	}
	if result.Provider != "" {
		sig.TranslationProvider = result.Provider
	}
	if result.Confidence > 0 {
		sig.TranslationConfidence = result.Confidence
	}
	if result.OriginalLanguage != "" {
		sig.Languages = []string{result.OriginalLanguage}
	}
	return sig
}

type passThroughTranslator struct{}

func (passThroughTranslator) Translate(sig signal.Signal) TranslationResult {
	lang := normalizedLanguage(sig)
	original := strings.TrimSpace(sig.OriginalText)
	if original == "" {
		original = sourceText(sig)
	}

	translated := strings.TrimSpace(sig.Translated)
	if payload := translatedPayload(sig.Raw); payload != "" {
		translated = payload
	}

	switch {
	case translated != "" && lang == "en":
		return TranslationResult{
			OriginalText:     firstNonEmpty(original, translated),
			TranslatedText:   translated,
			OriginalLanguage: "en",
			Provider:         firstNonEmpty(strings.TrimSpace(sig.TranslationProvider), "identity"),
			Confidence:       maxFloat(sig.TranslationConfidence, 1),
		}
	case translated != "" && translated != original:
		return TranslationResult{
			OriginalText:     original,
			TranslatedText:   translated,
			OriginalLanguage: lang,
			Provider:         firstNonEmpty(strings.TrimSpace(sig.TranslationProvider), "source_payload"),
			Confidence:       maxFloat(sig.TranslationConfidence, 0.88),
		}
	case lang == "en":
		return TranslationResult{
			OriginalText:     original,
			TranslatedText:   firstNonEmpty(translated, original),
			OriginalLanguage: "en",
			Provider:         firstNonEmpty(strings.TrimSpace(sig.TranslationProvider), "identity"),
			Confidence:       maxFloat(sig.TranslationConfidence, 1),
		}
	default:
		return TranslationResult{
			OriginalText:     original,
			TranslatedText:   firstNonEmpty(translated, original),
			OriginalLanguage: lang,
			Provider:         firstNonEmpty(strings.TrimSpace(sig.TranslationProvider), "pass_through"),
			Confidence:       maxFloat(sig.TranslationConfidence, 0.32),
		}
	}
}

func EnsureTranslatedText(sig signal.Signal) string {
	return applyTranslation(sig).Translated
}

func translatedPayload(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	for _, key := range []string{"translated", "translated_text", "translation", "english", "text_en", "summary_en"} {
		if value, ok := object[key].(string); ok {
			if cleaned, _ := sanitize.ExternalText(value); strings.TrimSpace(cleaned) != "" {
				return strings.TrimSpace(cleaned)
			}
		}
	}
	return ""
}

func normalizedLanguage(sig signal.Signal) string {
	if len(sig.Languages) == 0 {
		return "en"
	}
	lang := strings.TrimSpace(strings.ToLower(sig.Languages[0]))
	if lang == "" {
		return "en"
	}
	return lang
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func maxFloat(values ...float64) float64 {
	maximum := 0.0
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}
