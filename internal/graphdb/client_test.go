package graphdb

import (
	"testing"
	"time"

	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

func TestNewFromEnvReturnsNilWhenNotConfigured(t *testing.T) {
	t.Setenv("NEO4J_URI", "")
	t.Setenv("NEO4J_PASSWORD", "")

	client, err := NewFromEnv(t.Context())
	if err != nil {
		t.Fatalf("expected nil error when neo4j is not configured, got %v", err)
	}
	if client != nil {
		t.Fatal("expected nil client when neo4j is not configured")
	}
}

func TestSchemaStatementsIncludeCoreConstraints(t *testing.T) {
	statements := schemaStatements()
	if len(statements) < 10 {
		t.Fatalf("expected core graph schema statements, got %d", len(statements))
	}

	required := []string{
		"Signal",
		"Thesis",
		"ThesisVerdict",
		"Position",
		"Outcome",
		"Attribution",
		"Desk",
		"CouncilVoice",
		"CompetenceState",
		"QuantQuery",
		"Entity",
		"Instrument",
		"Source",
		"Domain",
		"EvidenceAssessment",
	}
	for _, want := range required {
		found := false
		for _, statement := range statements {
			if contains(statement, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected schema statement mentioning %s", want)
		}
	}
}

func TestEntityNodeIDNormalizesAliasInput(t *testing.T) {
	id := entityNodeID(signal.Entity{Name: " 英伟达 ", Type: "company"})
	if id != "company:NVDA" {
		t.Fatalf("unexpected entity id: %s", id)
	}
}

func TestInstrumentNodeIDUsesCanonicalKey(t *testing.T) {
	instrument := model.Instrument{
		Symbol:   "NVDA",
		SecType:  "OPT",
		Exchange: "SMART",
		Currency: "USD",
		Expiry:   "20260619",
		Strike:   140,
		Right:    "C",
		ConID:    123,
	}
	if got, want := instrumentNodeID(instrument), instrument.Key(); got != want {
		t.Fatalf("expected canonical instrument key, got %s want %s", got, want)
	}
}

func TestPrimaryLanguageDefaultsToEnglish(t *testing.T) {
	if got := primaryLanguage(signal.Signal{}); got != "en" {
		t.Fatalf("expected english fallback, got %s", got)
	}
	if got := primaryLanguage(signal.Signal{Languages: []string{"AR"}}); got != "ar" {
		t.Fatalf("expected normalized lower-case language, got %s", got)
	}
}

func TestEvidenceHelpersRespectPresence(t *testing.T) {
	meta := &evidence.Metadata{SourceTrust: 0.9}
	if got := evidenceFloat(meta, func() float64 { return meta.SourceTrust }); got != 0.9 {
		t.Fatalf("expected evidence float, got %.2f", got)
	}
	empty := &evidence.Metadata{}
	if got := evidenceString(empty, func() string { return "x" }); got != "" {
		t.Fatalf("expected empty evidence string, got %q", got)
	}
}

func TestNormalizeTimeUsesFallback(t *testing.T) {
	fallback := time.Now().UTC().Add(-time.Hour)
	if got := normalizeTime(time.Time{}, fallback); !got.Equal(fallback) {
		t.Fatalf("expected fallback time")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || stringContains(haystack, needle))
}

func stringContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
