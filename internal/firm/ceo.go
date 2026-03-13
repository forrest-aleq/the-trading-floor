package firm

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/pkg/model"
)

// CEO is the referee — capital reallocation, cross-desk correlation monitoring,
// regime shift detection. Does NOT trade, research, or route signals.
type CEO struct {
	log         *slog.Logger
	book        *book.Book
	beliefs     *belief.Graph
	desks       []*Desk
	floor       *Floor
	interval    time.Duration
	killSwitch  float64 // portfolio drawdown pct that triggers halt

	// Meta-beliefs: per-desk performance tracking
	deskSharpe map[string][]float64 // rolling daily returns for Sharpe calc
}

func NewCEO(bk *book.Book, beliefs *belief.Graph, floor *Floor) *CEO {
	return &CEO{
		log:        slog.Default().With("component", "ceo"),
		book:       bk,
		beliefs:    beliefs,
		floor:      floor,
		interval:   1 * time.Hour,
		killSwitch: 0.15, // 15% drawdown
		deskSharpe: make(map[string][]float64),
	}
}

func (c *CEO) SetDesks(desks []*Desk) {
	c.desks = desks
}

// Run starts the CEO's monitoring loop.
func (c *CEO) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.log.Info("CEO referee started", "interval", c.interval, "kill_switch", c.killSwitch)

	for {
		select {
		case <-ctx.Done():
			c.log.Info("CEO referee stopped")
			return
		case <-ticker.C:
			c.evaluate(ctx)
		}
	}
}

func (c *CEO) evaluate(ctx context.Context) {
	snapshot := c.book.Snapshot()

	// Kill switch: halt all trading if drawdown exceeds threshold
	if snapshot.MaxDrawdown >= c.killSwitch {
		c.log.Error("KILL SWITCH TRIGGERED — halting all trading",
			"drawdown", snapshot.MaxDrawdown,
			"threshold", c.killSwitch,
			"nav", snapshot.NAV,
		)
		return
	}

	// Cross-desk correlation check
	c.checkCorrelation(snapshot)

	// Desk performance evaluation
	c.evaluateDesks(snapshot)

	// Log portfolio health
	c.log.Info("portfolio health",
		"nav", snapshot.NAV,
		"cash", snapshot.Cash,
		"gross_exposure", snapshot.GrossExposure,
		"net_exposure", snapshot.NetExposure,
		"drawdown", snapshot.MaxDrawdown,
		"daily_pnl", snapshot.DailyPnL,
		"open_positions", snapshot.OpenPositions,
		"total_trades", snapshot.TotalTrades,
	)
}

// checkCorrelation detects when too many desks lean the same direction.
func (c *CEO) checkCorrelation(snap book.PortfolioSnapshot) {
	if snap.NAV <= 0 {
		return
	}

	// Net exposure as pct of NAV
	netPct := math.Abs(snap.NetExposure) / snap.NAV * 100
	if netPct > 25 {
		c.log.Warn("high directional concentration",
			"net_exposure_pct", netPct,
			"threshold", 25.0,
			"net_exposure", snap.NetExposure,
		)
	}
}

// evaluateDesks scores each desk and logs recommendations.
func (c *CEO) evaluateDesks(snap book.PortfolioSnapshot) {
	type deskScore struct {
		id     string
		pnl    float64
		trades int
		sharpe float64
	}

	var scores []deskScore
	for id, pnl := range snap.DeskPnL {
		positions := snap.DeskPositions[id]
		capital := snap.DeskCapital[id]

		// Record daily return for Sharpe calculation
		dailyReturn := 0.0
		if capital > 0 {
			dailyReturn = pnl / capital
		}
		c.deskSharpe[id] = append(c.deskSharpe[id], dailyReturn)
		// Keep last 30 observations
		if len(c.deskSharpe[id]) > 30 {
			c.deskSharpe[id] = c.deskSharpe[id][len(c.deskSharpe[id])-30:]
		}

		scores = append(scores, deskScore{
			id:     id,
			pnl:    pnl,
			trades: positions,
			sharpe: rollingSharpeSafe(c.deskSharpe[id]),
		})
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].sharpe > scores[j].sharpe
	})

	for _, ds := range scores {
		level := "info"
		if ds.sharpe < -0.5 {
			level = "warn"
		}
		if level == "warn" {
			c.log.Warn("desk underperforming",
				"desk", ds.id,
				"pnl", ds.pnl,
				"sharpe_30d", ds.sharpe,
				"open_positions", ds.trades,
			)
		}
	}
}

// ForceRegimeShift drops all desks to restricted autonomy on regime change.
func (c *CEO) ForceRegimeShift(newRegime model.Regime) {
	c.log.Warn("REGIME SHIFT — dropping all autonomy",
		"new_regime", newRegime.Key(),
	)

	for _, desk := range c.desks {
		desk.SetRegime(newRegime)
	}

	// Drop all autonomy on regime shift
	c.beliefs.DropAutonomy(newRegime.Key())
}

// CapitalReallocation adjusts desk capital based on risk-adjusted performance.
// Called weekly by the main loop or manually.
func (c *CEO) CapitalReallocation() {
	snapshot := c.book.Snapshot()
	totalCapital := snapshot.NAV

	type deskPerf struct {
		id     string
		sharpe float64
		weight float64
	}

	var perfs []deskPerf
	totalSharpe := 0.0

	for _, desk := range c.desks {
		returns := c.deskSharpe[desk.ID]
		s := rollingSharpeSafe(returns)
		// Floor: minimum 0.5 weight even for worst performers (prevents starvation)
		w := math.Max(0.5, s+1.0)
		perfs = append(perfs, deskPerf{id: desk.ID, sharpe: s, weight: w})
		totalSharpe += w
	}

	if totalSharpe <= 0 || len(perfs) == 0 {
		return
	}

	for _, p := range perfs {
		newCapital := (p.weight / totalSharpe) * totalCapital
		c.book.SetDeskCapital(p.id, newCapital)
		c.log.Info("capital reallocated",
			"desk", p.id,
			"sharpe", p.sharpe,
			"new_capital", newCapital,
		)
	}
}

func rollingSharpeSafe(returns []float64) float64 {
	if len(returns) < 5 {
		return 0
	}

	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	variance := 0.0
	for _, r := range returns {
		diff := r - mean
		variance += diff * diff
	}
	variance /= float64(len(returns))
	stddev := math.Sqrt(variance)

	if stddev < 1e-10 {
		return 0
	}

	// Annualized: mean * 252 / (stddev * sqrt(252))
	return mean / stddev * math.Sqrt(252)
}
