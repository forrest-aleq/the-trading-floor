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
	Host          string
	Port          int
	ClientID      int
	ClientIDTries int
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

	return Config{Host: host, Port: port, ClientID: clientID, ClientIDTries: clientIDTries}
}

type Connection struct {
	cfg Config
	log *slog.Logger

	mu sync.RWMutex
	ib *ibsync.IB
}

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
		ib, lastErr = connectIB(ctx, c.cfg.Host, c.cfg.Port, candidateID)
		if lastErr == nil {
			chosenID = candidateID
			break
		}
		if !isClientIDConflict(lastErr) {
			return fmt.Errorf("connect gateway: %w", lastErr)
		}
		c.log.Warn("IBKR client id unavailable, trying next id",
			"client_id", candidateID,
			"error", lastErr,
		)
	}
	if lastErr != nil && ib == nil {
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
	c.log.Info("connected to IBKR Gateway")

	if err := c.validateGateway(); err != nil {
		_ = ib.Disconnect()
		c.ib = nil
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

	// Verify we can read account summary (detects read-only API mode)
	summary := ib.AccountSummary()
	if len(summary) == 0 {
		c.log.Warn("empty account summary — API may be in read-only mode")
	}

	return nil
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
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.IsConnected() {
				continue
			}
			if err := c.Connect(ctx); err != nil {
				c.log.Warn("reconnect failed", "error", err)
			}
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
