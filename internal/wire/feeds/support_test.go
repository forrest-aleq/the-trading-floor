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
