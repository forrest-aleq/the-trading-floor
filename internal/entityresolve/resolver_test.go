package entityresolve

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/signal"
)

func TestResolveKnownMultilingualAlias(t *testing.T) {
	resolved := Resolve(signal.Entity{Name: "英伟达", Type: "company"}, "zh")
	if resolved.CanonicalID != "company:NVDA" {
		t.Fatalf("unexpected canonical id: %s", resolved.CanonicalID)
	}
	if resolved.Script != "han" {
		t.Fatalf("expected han script, got %s", resolved.Script)
	}
}

func TestResolveFallsBackToEntityID(t *testing.T) {
	resolved := Resolve(signal.Entity{Name: "Acme Corp", Type: "company", ID: "acme-1"}, "en")
	if resolved.CanonicalID != "company:ACME-1" {
		t.Fatalf("unexpected fallback id: %s", resolved.CanonicalID)
	}
	if resolved.Confidence != 0.75 {
		t.Fatalf("unexpected fallback confidence: %.2f", resolved.Confidence)
	}
}
