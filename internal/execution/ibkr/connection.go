package ibkr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Config for IBKR Gateway connection
type Config struct {
	Host     string // Gateway host (default: 127.0.0.1)
	Port     int    // 4002 for paper, 4001 for live
	ClientID int    // Unique client ID for this connection
}

func DefaultConfig() Config {
	host := os.Getenv("IBKR_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := 4002 // Paper trading default
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
	return Config{Host: host, Port: port, ClientID: clientID}
}

// Connection manages the IBKR Gateway connection lifecycle
type Connection struct {
	cfg       Config
	log       *slog.Logger
	connected atomic.Bool
	mu        sync.RWMutex

	// Callbacks
	onConnected    func()
	onDisconnected func()
}

func NewConnection(cfg Config) *Connection {
	return &Connection{
		cfg: cfg,
		log: slog.Default().With("component", "ibkr"),
	}
}

func (c *Connection) Connect(ctx context.Context) error {
	c.log.Info("connecting to IBKR Gateway",
		"host", c.cfg.Host,
		"port", c.cfg.Port,
		"client_id", c.cfg.ClientID,
	)

	// TODO: Use scmhub/ibapi to establish socket connection
	// ic := ibapi.NewIBClient(wrapper)
	// err := ic.Connect(c.cfg.Host, c.cfg.Port, c.cfg.ClientID)

	c.connected.Store(true)
	c.log.Info("connected to IBKR Gateway")

	if c.onConnected != nil {
		c.onConnected()
	}

	return nil
}

func (c *Connection) Disconnect() {
	c.log.Info("disconnecting from IBKR Gateway")
	c.connected.Store(false)
	if c.onDisconnected != nil {
		c.onDisconnected()
	}
}

func (c *Connection) IsConnected() bool {
	return c.connected.Load()
}

func (c *Connection) IsPaper() bool {
	return c.cfg.Port == 4002
}

// Reconnect loop — runs in background, reconnects on disconnect
func (c *Connection) RunReconnectLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !c.IsConnected() {
			c.log.Warn("IBKR disconnected, attempting reconnect...")
			if err := c.Connect(ctx); err != nil {
				c.log.Error("reconnect failed", "error", err)
				time.Sleep(5 * time.Second)
				continue
			}
		}
		time.Sleep(1 * time.Second)
	}
}

func (c *Connection) String() string {
	mode := "LIVE"
	if c.IsPaper() {
		mode = "PAPER"
	}
	return fmt.Sprintf("IBKR[%s:%d %s client=%d]", c.cfg.Host, c.cfg.Port, mode, c.cfg.ClientID)
}
