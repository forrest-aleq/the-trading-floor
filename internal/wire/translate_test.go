package wire

import (
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
