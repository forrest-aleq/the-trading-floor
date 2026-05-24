package firm

import (
	"testing"

	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/pkg/signal"
)

func TestScannerRejectionEventCapturesAuditMetadata(t *testing.T) {
	entry := scannerRejectionEvent("desk-macro-a", "macro", signal.Signal{
		ID:         "sig-reject",
		Source:     "fed-press",
		Type:       signal.TypeNews,
		Category:   "macro",
		Urgency:    0.42,
		Translated: "Federal Reserve commentary matches consensus and has no edge.",
	}, scanner.Evaluation{
		Reason:    "score_below_threshold",
		Score:     61,
		Tradeable: true,
	})

	if entry.EventType != "scanner_rejected" {
		t.Fatalf("expected scanner_rejected event type, got %q", entry.EventType)
	}
	if entry.DeskID != "desk-macro-a" || entry.Severity != "info" {
		t.Fatalf("unexpected event envelope: %+v", entry)
	}
	if entry.Metadata["signal_id"] != "sig-reject" {
		t.Fatalf("expected signal id metadata, got %+v", entry.Metadata)
	}
	if entry.Metadata["scanner_reason"] != "score_below_threshold" {
		t.Fatalf("expected scanner reason metadata, got %+v", entry.Metadata)
	}
	if entry.Metadata["scanner_score"] != float64(61) {
		t.Fatalf("expected scanner score metadata, got %+v", entry.Metadata)
	}
}

func TestScannerRejectionSeverityWarnsOnInfrastructureFailures(t *testing.T) {
	for _, reason := range []string{"llm_error", "parse_error", "llm_cooldown"} {
		if got := scannerRejectionSeverity(reason); got != "warn" {
			t.Fatalf("expected warn severity for %s, got %q", reason, got)
		}
	}
	if got := scannerRejectionSeverity("scanner_rejected"); got != "info" {
		t.Fatalf("expected info severity for scanner_rejected, got %q", got)
	}
}
