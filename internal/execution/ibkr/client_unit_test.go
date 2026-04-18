package ibkr

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/scmhub/ibsync"
)

type fakeConnection struct {
	mu           sync.Mutex
	connectErr   error
	connectCalls int
	loopCalls    int
}

func (f *fakeConnection) Connect(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectCalls++
	return f.connectErr
}

func (f *fakeConnection) Disconnect() {}

func (f *fakeConnection) IB() *ibsync.IB { return nil }

func (f *fakeConnection) IsConnected() bool { return false }

func (f *fakeConnection) IsPaper() bool { return true }

func (f *fakeConnection) Status() ConnectionStatus { return ConnectionStatus{} }

func (f *fakeConnection) RunReconnectLoop(ctx context.Context) {
	f.mu.Lock()
	f.loopCalls++
	f.mu.Unlock()
	<-ctx.Done()
}

func (f *fakeConnection) counts() (connects int, loops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connectCalls, f.loopCalls
}

func TestClientConnectStartsReconnectLoopOnInitialFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &fakeConnection{connectErr: errors.New("unavailable")}
	client := &Client{conn: conn, log: slog.Default()}

	if err := client.Connect(ctx); err == nil {
		t.Fatal("expected connect error")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		connects, loops := conn.counts()
		if connects == 1 && loops == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	connects, loops := conn.counts()
	t.Fatalf("expected one connect and one reconnect loop start, got connects=%d loops=%d", connects, loops)
}

func TestClientConnectStartsReconnectLoopOnlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &fakeConnection{connectErr: errors.New("unavailable")}
	client := &Client{conn: conn, log: slog.Default()}

	_ = client.Connect(ctx)
	_ = client.Connect(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		connects, loops := conn.counts()
		if connects == 2 && loops == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	connects, loops := conn.counts()
	t.Fatalf("expected two connect attempts and one reconnect loop start, got connects=%d loops=%d", connects, loops)
}

func TestRunBlockingIBCallRespectsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := runBlockingIBCall(ctx, func() error {
		<-make(chan struct{})
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("expected blocking call to stop promptly, took %s", elapsed)
	}
}
