package kalshi

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type liveSafetyConfig struct {
	KillSwitch        bool
	KillSwitchFile    string
	MaxOrders         int
	MaxRiskCents      int64
	DisableAfterFirst bool
	DuplicateCooldown time.Duration
	AllowedDesks      map[string]struct{}
	AllowedTickers    map[string]struct{}
}

type liveSafetyGuard struct {
	cfg liveSafetyConfig

	mu             sync.Mutex
	sessionOrders  int
	sessionRisk    int64
	recentIntents  map[string]time.Time
	disabledByOnce bool
}

func newLiveSafetyGuardFromEnv() *liveSafetyGuard {
	cfg := liveSafetyConfig{
		KillSwitch: readAnyBoolEnv(false,
			"KALSHI_LIVE_KILL_SWITCH",
			"KALSHI_KILL_SWITCH",
			"FLOOR_GLOBAL_KILL_SWITCH",
			"FLOOR_DISABLE_TRADING",
			"TRADING_KILL_SWITCH",
		),
		KillSwitchFile: firstNonEmptyEnv(
			"KALSHI_LIVE_KILL_SWITCH_FILE",
			"KALSHI_KILL_SWITCH_FILE",
			"FLOOR_GLOBAL_KILL_SWITCH_FILE",
		),
		MaxOrders:         readIntEnv("KALSHI_LIVE_MAX_ORDERS_PER_SESSION", 1),
		MaxRiskCents:      parseDollarEnvCents("KALSHI_LIVE_MAX_RISK_DOLLARS_PER_SESSION", "1.00"),
		DisableAfterFirst: readBoolEnv("KALSHI_LIVE_DISABLE_AFTER_FIRST", true),
		DuplicateCooldown: readDurationEnv("KALSHI_LIVE_DUPLICATE_COOLDOWN", 15*time.Minute),
		AllowedDesks:      parseAllowlistEnv("KALSHI_LIVE_ALLOWED_DESKS", strings.ToLower),
		AllowedTickers:    parseAllowlistEnv("KALSHI_LIVE_ALLOWED_TICKERS", strings.ToUpper),
	}
	if cfg.MaxOrders <= 0 {
		cfg.MaxOrders = 1
	}
	if cfg.MaxRiskCents <= 0 {
		cfg.MaxRiskCents = 100
	}
	return &liveSafetyGuard{
		cfg:           cfg,
		recentIntents: make(map[string]time.Time),
	}
}

func (g *liveSafetyGuard) reserve(mapped MappedOrder, at time.Time) error {
	if g == nil {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if g.killSwitchEnabled() {
		return fmt.Errorf("kalshi_live_kill_switch_enabled")
	}

	deskID := strings.ToLower(strings.TrimSpace(mapped.DeskID))
	if len(g.cfg.AllowedDesks) > 0 {
		if _, ok := g.cfg.AllowedDesks[deskID]; !ok {
			return fmt.Errorf("kalshi_live_desk_not_allowed: %s", mapped.DeskID)
		}
	}

	ticker := strings.ToUpper(strings.TrimSpace(mapped.Request.Ticker))
	if len(g.cfg.AllowedTickers) > 0 {
		if _, ok := g.cfg.AllowedTickers[ticker]; !ok {
			return fmt.Errorf("kalshi_live_ticker_not_allowed: %s", mapped.Request.Ticker)
		}
	}

	side := strings.ToLower(strings.TrimSpace(mapped.Request.Side))
	intentKey := ticker + "|" + side

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.disabledByOnce || (g.cfg.DisableAfterFirst && g.sessionOrders > 0) {
		return fmt.Errorf("kalshi_live_canary_already_used")
	}
	if g.cfg.MaxOrders > 0 && g.sessionOrders >= g.cfg.MaxOrders {
		return fmt.Errorf("kalshi_live_session_order_cap_reached: %d/%d", g.sessionOrders, g.cfg.MaxOrders)
	}
	if g.cfg.MaxRiskCents > 0 && g.sessionRisk+mapped.EstimatedRiskCents > g.cfg.MaxRiskCents {
		return fmt.Errorf("kalshi_live_session_risk_cap_reached: current=%s next=%s cap=%s",
			FormatCents(g.sessionRisk),
			FormatCents(mapped.EstimatedRiskCents),
			FormatCents(g.cfg.MaxRiskCents),
		)
	}
	if g.cfg.DuplicateCooldown > 0 {
		if previous, ok := g.recentIntents[intentKey]; ok && at.Sub(previous) < g.cfg.DuplicateCooldown {
			return fmt.Errorf("kalshi_live_duplicate_intent_cooldown: %s cooldown=%s", intentKey, g.cfg.DuplicateCooldown)
		}
	}

	g.sessionOrders++
	g.sessionRisk += mapped.EstimatedRiskCents
	if intentKey != "|" {
		g.recentIntents[intentKey] = at
	}
	if g.cfg.DisableAfterFirst {
		g.disabledByOnce = true
	}
	return nil
}

func (g *liveSafetyGuard) killSwitchEnabled() bool {
	if g == nil {
		return false
	}
	if g.cfg.KillSwitch {
		return true
	}
	path := strings.TrimSpace(g.cfg.KillSwitchFile)
	if path == "" {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		return true
	}
	value := strings.ToLower(strings.TrimSpace(string(raw)))
	switch value {
	case "", "1", "true", "yes", "on", "kill", "disabled", "disable":
		return true
	case "0", "false", "no", "off", "normal", "enabled", "enable":
		return false
	default:
		return true
	}
}

func readAnyBoolEnv(fallback bool, names ...string) bool {
	for _, name := range names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return fallback
		}
		return parsed
	}
	return fallback
}

func readIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func readDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value != "" {
			return value
		}
	}
	return ""
}

func parseAllowlistEnv(name string, normalize func(string) string) map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	out := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if normalize != nil {
			value = normalize(value)
		}
		out[value] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
