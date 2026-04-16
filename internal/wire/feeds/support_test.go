package feeds

import (
	"net/http"
	"testing"
	"time"
)

func TestNewFeedHTTPClientUsesConfiguredTimeouts(t *testing.T) {
	t.Setenv("WIRE_HTTP_TIMEOUT", "35s")
	t.Setenv("WIRE_DIAL_TIMEOUT", "11s")
	t.Setenv("WIRE_TLS_HANDSHAKE_TIMEOUT", "12s")
	t.Setenv("WIRE_RESPONSE_HEADER_TIMEOUT", "13s")

	client := newFeedHTTPClient()
	if client.Timeout != 35*time.Second {
		t.Fatalf("expected http timeout 35s, got %s", client.Timeout)
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}

	if transport.TLSHandshakeTimeout != 12*time.Second {
		t.Fatalf("expected TLS timeout 12s, got %s", transport.TLSHandshakeTimeout)
	}
	if transport.ResponseHeaderTimeout != 13*time.Second {
		t.Fatalf("expected response header timeout 13s, got %s", transport.ResponseHeaderTimeout)
	}
	if transport.MaxConnsPerHost != 32 {
		t.Fatalf("expected max conns per host 32, got %d", transport.MaxConnsPerHost)
	}
}

func TestReadFeedDurationFallsBackOnInvalidInput(t *testing.T) {
	t.Setenv("WIRE_HTTP_TIMEOUT", "nope")
	if got := readFeedDuration("WIRE_HTTP_TIMEOUT", 42*time.Second); got != 42*time.Second {
		t.Fatalf("expected fallback duration, got %s", got)
	}
}

func TestSignalTimestampParsesSingleDigitRFC1123Day(t *testing.T) {
	got := signalTimestamp("Tue, 7 Apr 2026 21:50:00 GMT")
	want := time.Date(2026, time.April, 7, 21, 50, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("expected %s, got %s", want.Format(time.RFC3339), got.Format(time.RFC3339))
	}
}

func TestSignalTimestampInfersDateFromHintsWhenPubDateMissing(t *testing.T) {
	got := signalTimestamp("", "https://www.federalreserve.gov/newsevents/speech/jefferson20260407a.htm")
	want := time.Date(2026, time.April, 7, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("expected %s, got %s", want.Format(time.RFC3339), got.Format(time.RFC3339))
	}
}
