package ibkr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scmhub/ibsync"
)

// Config for IBKR Gateway connection.
type Config struct {
	Host               string
	Port               int
	ClientID           int
	ClientIDTries      int
	ReconnectBaseDelay time.Duration
	ReconnectMaxDelay  time.Duration
}

func DefaultConfig() Config {
	host := os.Getenv("IBKR_HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	port := 4002
	if p := os.Getenv("IBKR_PORT"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil {
			port = parsed
		}
	}

	clientID := 1
	if c := os.Getenv("IBKR_CLIENT_ID"); c != "" {
		if parsed, err := strconv.Atoi(c); err == nil {
			clientID = parsed
		}
	}

	clientIDTries := 5
	if raw := os.Getenv("IBKR_CLIENT_ID_TRIES"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			clientIDTries = parsed
		}
	}

	reconnectBaseDelay := 5 * time.Second
	if raw := os.Getenv("IBKR_RECONNECT_BASE_DELAY"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			reconnectBaseDelay = parsed
		}
	}

	reconnectMaxDelay := time.Minute
	if raw := os.Getenv("IBKR_RECONNECT_MAX_DELAY"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			if parsed < reconnectBaseDelay {
				reconnectMaxDelay = reconnectBaseDelay
			} else {
				reconnectMaxDelay = parsed
			}
		}
	}

	return Config{
		Host:               host,
		Port:               port,
		ClientID:           clientID,
		ClientIDTries:      clientIDTries,
		ReconnectBaseDelay: reconnectBaseDelay,
		ReconnectMaxDelay:  reconnectMaxDelay,
	}
}

type Connection struct {
	cfg Config
	log *slog.Logger

	mu sync.RWMutex
	ib *ibsync.IB

	lastConnectErr  string
	lastAttemptAt   time.Time
	lastConnectedAt time.Time
}

type ConnectionStatus struct {
	Connected       bool
	Host            string
	Port            int
	ClientID        int
	LastConnectErr  string
	LastAttemptAt   time.Time
	LastConnectedAt time.Time
}

const accountSummaryProbeTimeout = 3 * time.Second

var connectIBFn = connectIB
var validateGatewayFn = func(c *Connection) error { return c.validateGateway() }

func NewConnection(cfg Config) *Connection {
	return &Connection{
		cfg: cfg,
		log: slog.Default().With("component", "ibkr"),
	}
}

func (c *Connection) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ib != nil && c.ib.IsConnected() {
		return nil
	}

	c.log.Info("connecting to IBKR Gateway",
		"host", c.cfg.Host,
		"port", c.cfg.Port,
		"client_id", c.cfg.ClientID,
		"client_id_tries", c.cfg.ClientIDTries,
	)

	startID := c.cfg.ClientID
	tries := c.cfg.ClientIDTries
	if tries <= 0 {
		tries = 1
	}

	var (
		ib       *ibsync.IB
		chosenID int
		lastErr  error
	)
	for offset := 0; offset < tries; offset++ {
		candidateID := startID + offset
		c.lastAttemptAt = time.Now().UTC()
		ib, lastErr = connectIBFn(ctx, c.cfg.Host, c.cfg.Port, candidateID)
		if lastErr == nil {
			chosenID = candidateID
			break
		}
		if !isClientIDConflict(lastErr) {
			c.lastConnectErr = lastErr.Error()
			return fmt.Errorf("connect gateway: %w", lastErr)
		}
		c.log.Warn("IBKR client id unavailable, trying next id",
			"client_id", candidateID,
			"error", lastErr,
		)
	}
	if lastErr != nil && ib == nil {
		if isClientIDConflict(lastErr) {
			c.cfg.ClientID = startID + tries
		}
		c.lastConnectErr = lastErr.Error()
		return fmt.Errorf("connect gateway: %w", lastErr)
	}

	select {
	case <-ctx.Done():
		_ = ib.Disconnect()
		return ctx.Err()
	default:
	}

	c.ib = ib
	c.cfg.ClientID = chosenID
	c.lastConnectErr = ""
	c.lastConnectedAt = time.Now().UTC()
	c.log.Info("connected to IBKR Gateway")

	if err := validateGatewayFn(c); err != nil {
		_ = ib.Disconnect()
		c.ib = nil
		c.lastConnectErr = err.Error()
		return fmt.Errorf("gateway validation: %w", err)
	}

	return nil
}

// validateGateway checks that the connected gateway is in a usable state.
func (c *Connection) validateGateway() error {
	ib := c.ib
	if ib == nil {
		return fmt.Errorf("not connected")
	}

	// Check managed accounts exist (proves the gateway is logged in)
	accounts := ib.ManagedAccounts()
	if len(accounts) == 0 {
		return fmt.Errorf("no managed accounts — gateway may not be logged in")
	}
	c.log.Info("gateway validated", "accounts", len(accounts))

	// Probe account summary without making it a startup hard-gate. TWS sessions
	// can take a while to surface summary data, and blocking here stalls the
	// entire daemon before the floor can start.
	if err := c.probeAccountSummary(ib); err != nil {
		c.log.Warn("gateway account summary probe incomplete", "error", err)
	}

	return nil
}

func (c *Connection) probeAccountSummary(ib *ibsync.IB) error {
	if ib == nil {
		return fmt.Errorf("not connected")
	}

	summaryCh := make(chan int, 1)
	go func() {
		summaryCh <- len(ib.AccountSummary())
	}()

	select {
	case count := <-summaryCh:
		if count == 0 {
			return fmt.Errorf("empty account summary — API may be in read-only mode")
		}
		c.log.Info("gateway account summary available", "items", count)
		return nil
	case <-time.After(accountSummaryProbeTimeout):
		return fmt.Errorf("account summary probe timed out after %s", accountSummaryProbeTimeout)
	}
}

func (c *Connection) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ib == nil {
		return
	}

	if err := c.ib.Disconnect(); err != nil {
		c.log.Warn("disconnect failed", "error", err)
	} else {
		c.log.Info("disconnected from IBKR Gateway")
	}
	c.ib = nil
}

func (c *Connection) Status() ConnectionStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	connected := false
	if c.ib != nil {
		connected = c.ib.IsConnected()
	}

	return ConnectionStatus{
		Connected:       connected,
		Host:            c.cfg.Host,
		Port:            c.cfg.Port,
		ClientID:        c.cfg.ClientID,
		LastConnectErr:  c.lastConnectErr,
		LastAttemptAt:   c.lastAttemptAt,
		LastConnectedAt: c.lastConnectedAt,
	}
}

func (c *Connection) IB() *ibsync.IB {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ib
}

func (c *Connection) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ib != nil && c.ib.IsConnected()
}

func (c *Connection) IsPaper() bool {
	return c.cfg.Port == 4002 || c.cfg.Port == 7497
}

func (c *Connection) RunReconnectLoop(ctx context.Context) {
	delay := c.cfg.ReconnectBaseDelay
	if delay <= 0 {
		delay = 5 * time.Second
	}
	maxDelay := c.cfg.ReconnectMaxDelay
	if maxDelay < delay {
		maxDelay = delay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if c.IsConnected() {
				delay = c.cfg.ReconnectBaseDelay
				if delay <= 0 {
					delay = 5 * time.Second
				}
				timer.Reset(delay)
				continue
			}
			if err := c.Connect(ctx); err != nil {
				c.log.Warn("reconnect failed", "error", err, "retry_in", delay)
				delay *= 2
				if delay > maxDelay {
					delay = maxDelay
				}
			} else {
				delay = c.cfg.ReconnectBaseDelay
				if delay <= 0 {
					delay = 5 * time.Second
				}
			}
			timer.Reset(delay)
		}
	}
}

func (c *Connection) String() string {
	mode := "LIVE"
	if c.IsPaper() {
		mode = "PAPER"
	}
	return fmt.Sprintf("IBKR[%s:%d %s client=%d]", c.cfg.Host, c.cfg.Port, mode, c.cfg.ClientID)
}

func connectIB(ctx context.Context, host string, port, clientID int) (*ibsync.IB, error) {
	ib := ibsync.NewIB()
	cfg := &ibsync.Config{
		Host:     host,
		Port:     port,
		ClientID: int64(clientID),
		InSync:   true,
		Timeout:  15 * time.Second,
	}
	if err := ib.Connect(cfg); err != nil {
		return nil, err
	}
	return ib, nil
}

func isClientIDConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "client id is already in use")
}
