package ibkr

import (
	"context"
	"testing"
	"time"

	"github.com/scmhub/ibsync"
)

func TestIsClientIDConflict(t *testing.T) {
	if !isClientIDConflict(assertErr("Unable to connect as the client id is already in use. Retry with a unique client id.")) {
		t.Fatal("expected client id conflict to be detected")
	}
	if isClientIDConflict(assertErr("some other gateway problem")) {
		t.Fatal("did not expect unrelated error to be treated as client id conflict")
	}
}

type staticErr string

func (e staticErr) Error() string { return string(e) }

func assertErr(message string) error { return staticErr(message) }

func TestDefaultConfigReadsReconnectDelays(t *testing.T) {
	t.Setenv("IBKR_RECONNECT_BASE_DELAY", "7s")
	t.Setenv("IBKR_RECONNECT_MAX_DELAY", "45s")

	cfg := DefaultConfig()
	if cfg.ReconnectBaseDelay != 7*time.Second {
		t.Fatalf("ReconnectBaseDelay = %s, want 7s", cfg.ReconnectBaseDelay)
	}
	if cfg.ReconnectMaxDelay != 45*time.Second {
		t.Fatalf("ReconnectMaxDelay = %s, want 45s", cfg.ReconnectMaxDelay)
	}
}

func TestDefaultConfigClampsReconnectMaxDelayToBase(t *testing.T) {
	t.Setenv("IBKR_RECONNECT_BASE_DELAY", "12s")
	t.Setenv("IBKR_RECONNECT_MAX_DELAY", "5s")

	cfg := DefaultConfig()
	if cfg.ReconnectBaseDelay != 12*time.Second {
		t.Fatalf("ReconnectBaseDelay = %s, want 12s", cfg.ReconnectBaseDelay)
	}
	if cfg.ReconnectMaxDelay != 12*time.Second {
		t.Fatalf("ReconnectMaxDelay = %s, want 12s", cfg.ReconnectMaxDelay)
	}
}

func TestConnectClientIDConflictAdvancesNextStartID(t *testing.T) {
	originalConnect := connectIBFn
	originalValidate := validateGatewayFn
	t.Cleanup(func() {
		connectIBFn = originalConnect
		validateGatewayFn = originalValidate
	})

	attempts := make([]int, 0, 3)
	connectIBFn = func(_ context.Context, _ string, _ int, clientID int) (*ibsync.IB, error) {
		attempts = append(attempts, clientID)
		return nil, assertErr("client id is already in use")
	}
	validateGatewayFn = func(*Connection) error { return nil }

	conn := NewConnection(Config{
		Host:               "127.0.0.1",
		Port:               4002,
		ClientID:           41,
		ClientIDTries:      3,
		ReconnectBaseDelay: 5 * time.Second,
		ReconnectMaxDelay:  time.Minute,
	})
	err := conn.Connect(context.Background())
	if err == nil {
		t.Fatal("expected connect to fail after exhausting client id retries")
	}
	if len(attempts) != 3 || attempts[0] != 41 || attempts[1] != 42 || attempts[2] != 43 {
		t.Fatalf("unexpected client id attempts: %#v", attempts)
	}
	if conn.cfg.ClientID != 44 {
		t.Fatalf("ClientID = %d, want 44 after exhausted retries", conn.cfg.ClientID)
	}
	if conn.lastConnectErr == "" {
		t.Fatal("expected lastConnectErr to be recorded")
	}
	if conn.lastAttemptAt.IsZero() {
		t.Fatal("expected lastAttemptAt to be recorded")
	}

	status := conn.Status()
	if status.ClientID != 44 {
		t.Fatalf("status.ClientID = %d, want 44", status.ClientID)
	}
	if status.LastConnectErr == "" {
		t.Fatal("expected status to include last connect error")
	}
}

func TestConnectNonConflictErrorRecordsFailureState(t *testing.T) {
	originalConnect := connectIBFn
	originalValidate := validateGatewayFn
	t.Cleanup(func() {
		connectIBFn = originalConnect
		validateGatewayFn = originalValidate
	})

	attempts := 0
	connectIBFn = func(_ context.Context, _ string, _ int, _ int) (*ibsync.IB, error) {
		attempts++
		return nil, assertErr("gateway unavailable")
	}
	validateGatewayFn = func(*Connection) error { return nil }

	conn := NewConnection(Config{Host: "127.0.0.1", Port: 4002, ClientID: 9, ClientIDTries: 5})
	err := conn.Connect(context.Background())
	if err == nil {
		t.Fatal("expected connect to fail")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if conn.cfg.ClientID != 9 {
		t.Fatalf("ClientID = %d, want unchanged 9", conn.cfg.ClientID)
	}
	if conn.lastConnectErr != "gateway unavailable" {
		t.Fatalf("lastConnectErr = %q, want gateway unavailable", conn.lastConnectErr)
	}
}

func TestConnectSuccessAfterClientIDConflictUpdatesChosenID(t *testing.T) {
	originalConnect := connectIBFn
	originalValidate := validateGatewayFn
	t.Cleanup(func() {
		connectIBFn = originalConnect
		validateGatewayFn = originalValidate
	})

	attempts := make([]int, 0, 3)
	connectIBFn = func(_ context.Context, _ string, _ int, clientID int) (*ibsync.IB, error) {
		attempts = append(attempts, clientID)
		if clientID < 23 {
			return nil, assertErr("client id is already in use")
		}
		return &ibsync.IB{}, nil
	}
	validateGatewayFn = func(*Connection) error { return nil }

	conn := NewConnection(Config{Host: "127.0.0.1", Port: 4002, ClientID: 21, ClientIDTries: 5})
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	if len(attempts) != 3 || attempts[0] != 21 || attempts[1] != 22 || attempts[2] != 23 {
		t.Fatalf("unexpected client id attempts: %#v", attempts)
	}
	if conn.cfg.ClientID != 23 {
		t.Fatalf("ClientID = %d, want 23", conn.cfg.ClientID)
	}
	if conn.lastConnectErr != "" {
		t.Fatalf("lastConnectErr = %q, want empty string", conn.lastConnectErr)
	}
	if conn.lastConnectedAt.IsZero() {
		t.Fatal("expected lastConnectedAt to be recorded")
	}
	if conn.IB() == nil {
		t.Fatal("expected successful connect to retain ib handle")
	}
}
