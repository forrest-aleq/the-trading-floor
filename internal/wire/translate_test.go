package wire

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestNormalizeSignalPreservesOriginalTextAndExtractsSourceTranslation(t *testing.T) {
	sig := NormalizeSignal(signal.Signal{
		ID:        "sig-zh-1",
		Source:    "telegram/mena",
		Type:      signal.TypeNews,
		Category:  "geopolitical",
		Languages: []string{"zh"},
		Raw: []byte(`{
			"title":"英伟达发布新的数据中心路线图",
			"translated_text":"NVIDIA unveils a new data center roadmap"
		}`),
	})

	if sig.OriginalText != "英伟达发布新的数据中心路线图" {
		t.Fatalf("unexpected original text: %q", sig.OriginalText)
	}
	if sig.Translated != "NVIDIA unveils a new data center roadmap" {
		t.Fatalf("unexpected translated text: %q", sig.Translated)
	}
	if sig.TranslationProvider != "source_payload" {
		t.Fatalf("expected source payload provider, got %q", sig.TranslationProvider)
	}
	if sig.TranslationConfidence < 0.8 {
		t.Fatalf("expected translation confidence >= 0.8, got %.2f", sig.TranslationConfidence)
	}
	if sig.EvidenceMeta == nil || sig.EvidenceMeta.TranslationProvider != "source_payload" {
		t.Fatalf("expected evidence metadata to carry translation provider, got %+v", sig.EvidenceMeta)
	}
}

func TestNormalizeSignalFallsBackToPassThroughForNonEnglishWithoutTranslation(t *testing.T) {
	sig := NormalizeSignal(signal.Signal{
		ID:           "sig-ar-1",
		Source:       "telegram/mena",
		Type:         signal.TypeNews,
		Category:     "geopolitical",
		Languages:    []string{"ar"},
		OriginalText: "تعطل الشحن في الممر البحري",
	})

	if sig.Translated != sig.OriginalText {
		t.Fatalf("expected pass-through translated text, got %q", sig.Translated)
	}
	if sig.TranslationProvider != "pass_through" {
		t.Fatalf("expected pass_through provider, got %q", sig.TranslationProvider)
	}
	if sig.TranslationConfidence <= 0 || sig.TranslationConfidence >= 0.5 {
		t.Fatalf("expected low-confidence pass-through translation, got %.2f", sig.TranslationConfidence)
	}
}

func TestDeepLTranslatorUsesAPIResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "DeepL-Auth-Key test-key" {
			t.Errorf("unexpected auth header: %s", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		texts, _ := payload["text"].([]any)
		if len(texts) != 1 || texts[0] != "اضطراب حركة الملاحة في مضيق هرمز 15%" {
			t.Errorf("unexpected translation payload: %+v", payload)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"translations": []map[string]any{{
				"detected_source_language": "AR",
				"text":                     "Shipping traffic disrupted in the Strait of Hormuz 15%",
			}},
		})
	}))
	defer server.Close()

	translator := &deeplTranslator{
		http:       server.Client(),
		endpoint:   server.URL,
		authKey:    "test-key",
		targetLang: "EN",
		modelType:  "latency_optimized",
	}

	result := translator.Translate(signal.Signal{
		ID:           "sig-deepl-1",
		Source:       "telegram/mena",
		Type:         signal.TypeNews,
		Category:     "geopolitical",
		Languages:    []string{"ar"},
		OriginalText: "اضطراب حركة الملاحة في مضيق هرمز 15%",
	})

	if result.Provider != "deepl" {
		t.Fatalf("expected deepl provider, got %q", result.Provider)
	}
	if result.TranslatedText != "Shipping traffic disrupted in the Strait of Hormuz 15%" {
		t.Fatalf("unexpected translated text: %q", result.TranslatedText)
	}
	if result.Confidence < 0.9 {
		t.Fatalf("expected high-confidence DeepL translation, got %.2f", result.Confidence)
	}
}
