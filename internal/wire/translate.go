package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

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
		case "deepl":
			defaultTranslator = newDeepLTranslatorFromEnv()
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

type deeplTranslator struct {
	http       *http.Client
	endpoint   string
	authKey    string
	targetLang string
	modelType  string
}

var translationNumberPattern = regexp.MustCompile(`\$?\b\d[\d,]*(?:\.\d+)?[mbkMBK]?%?\b`)

func (t *deeplTranslator) String() string {
	if t == nil {
		return "deeplTranslator<nil>"
	}
	return fmt.Sprintf("deeplTranslator{endpoint:%q,targetLang:%q,modelType:%q,authKey:%q}", t.endpoint, t.targetLang, t.modelType, "[redacted]")
}

func (t *deeplTranslator) GoString() string {
	return t.String()
}

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

func newDeepLTranslatorFromEnv() Translator {
	authKey := strings.TrimSpace(os.Getenv("DEEPL_API_KEY"))
	if authKey == "" {
		slog.Default().With("component", "wire-translate").Warn("DEEPL_API_KEY not set; falling back to pass-through translation")
		return passThroughTranslator{}
	}

	endpoint := strings.TrimSpace(os.Getenv("DEEPL_API_BASE_URL"))
	if endpoint == "" {
		endpoint = "https://api-free.deepl.com/v2/translate"
	}

	return &deeplTranslator{
		http: &http.Client{
			Timeout: 8 * time.Second,
		},
		endpoint:   endpoint,
		authKey:    authKey,
		targetLang: strings.ToUpper(firstNonEmpty(strings.TrimSpace(os.Getenv("WIRE_TRANSLATION_TARGET_LANG")), "EN")),
		modelType:  firstNonEmpty(strings.TrimSpace(os.Getenv("DEEPL_MODEL_TYPE")), "latency_optimized"),
	}
}

func (t *deeplTranslator) Translate(sig signal.Signal) TranslationResult {
	lang := normalizedLanguage(sig)
	original := strings.TrimSpace(sig.OriginalText)
	if original == "" {
		original = sourceText(sig)
	}
	if original == "" {
		return passThroughTranslator{}.Translate(sig)
	}

	result := passThroughTranslator{}.Translate(sig)
	if result.Provider == "source_payload" || (result.Provider == "identity" && lang == "en") {
		return result
	}
	if lang == "en" || lang == "und" {
		return passThroughTranslator{}.Translate(sig)
	}

	translated, detected, err := t.translateText(original, lang)
	if err != nil {
		slog.Default().With("component", "wire-translate").Warn("deepl translation failed; falling back to pass-through",
			"language", lang,
			"error", err,
		)
		return passThroughTranslator{}.Translate(sig)
	}

	confidence := 0.78 + (numericTranslationConfidence(original, translated) * 0.17)
	if detected != "" && normalizeLanguageCode(detected) == normalizeLanguageCode(lang) {
		confidence += 0.03
	}

	return TranslationResult{
		OriginalText:     original,
		TranslatedText:   strings.TrimSpace(translated),
		OriginalLanguage: normalizeLanguageCode(firstNonEmpty(detected, lang)),
		Provider:         "deepl",
		Confidence:       maxFloat(sig.TranslationConfidence, confidence),
	}
}

func EnsureTranslatedText(sig signal.Signal) string {
	return applyTranslation(sig).Translated
}

func (t *deeplTranslator) translateText(text, language string) (string, string, error) {
	payload := map[string]any{
		"text":                []string{text},
		"target_lang":         t.targetLang,
		"model_type":          t.modelType,
		"split_sentences":     "0",
		"preserve_formatting": true,
	}
	if sourceLang := deeplLanguageCode(language); sourceLang != "" {
		payload["source_lang"] = sourceLang
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("marshal deepl request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("build deepl request: %w", err)
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+t.authKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "trading-floor/1.0")

	resp, err := t.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("call deepl: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("deepl non-200: %s", resp.Status)
	}

	var decoded struct {
		Translations []struct {
			DetectedSourceLanguage string `json:"detected_source_language"`
			Text                   string `json:"text"`
		} `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", "", fmt.Errorf("decode deepl response: %w", err)
	}
	if len(decoded.Translations) == 0 || strings.TrimSpace(decoded.Translations[0].Text) == "" {
		return "", "", fmt.Errorf("deepl returned no translation")
	}

	return decoded.Translations[0].Text, decoded.Translations[0].DetectedSourceLanguage, nil
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

func deeplLanguageCode(language string) string {
	language = normalizeLanguageCode(language)
	if language == "" || language == "und" {
		return ""
	}
	return strings.ToUpper(language)
}

func numericTranslationConfidence(original, translated string) float64 {
	originalNumbers := normalizeNumbers(translationNumberPattern.FindAllString(original, -1))
	if len(originalNumbers) == 0 {
		return 1
	}
	translatedNumbers := normalizeNumbers(translationNumberPattern.FindAllString(translated, -1))
	if len(translatedNumbers) == 0 {
		return 0.35
	}

	used := make([]bool, len(translatedNumbers))
	matches := 0
	for _, want := range originalNumbers {
		for i, got := range translatedNumbers {
			if used[i] || want != got {
				continue
			}
			used[i] = true
			matches++
			break
		}
	}
	return float64(matches) / float64(len(originalNumbers))
}

func normalizeNumbers(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		value = strings.ReplaceAll(value, ",", "")
		value = strings.ReplaceAll(value, "$", "")
		normalized = append(normalized, value)
	}
	return normalized
}
