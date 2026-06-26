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

func TestResolveKnownTradingAliases(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		want string
	}{
		{name: "Ferrari", typ: "company", want: "company:RACE"},
		{name: "Micron Technology", typ: "company", want: "company:MU"},
		{name: "South Korea", typ: "etf", want: "etf:EWY"},
	}

	for _, tt := range tests {
		resolved := Resolve(signal.Entity{Name: tt.name, Type: tt.typ}, "en")
		if resolved.CanonicalID != tt.want {
			t.Fatalf("Resolve(%q) = %s, want %s", tt.name, resolved.CanonicalID, tt.want)
		}
	}
}
