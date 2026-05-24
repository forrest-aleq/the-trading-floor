package firm

import (
	"context"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/pkg/model"
)

// CEO is the referee — capital reallocation, cross-desk correlation monitoring,
// regime shift detection. Does NOT trade, research, or route signals.
type CEO struct {
	log        *slog.Logger
	book       *book.Book
	beliefs    *belief.Graph
	desks      []*Desk
	floor      *Floor
	interval   time.Duration
	killSwitch float64 // portfolio drawdown pct that triggers halt
	entryGate  *ManualEntryControl
	halted     bool

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

func (c *CEO) SetEntryControl(control *ManualEntryControl) {
	c.entryGate = control
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
	openPositions := c.book.GetOpenPositions()

	// Kill switch: halt all trading if drawdown exceeds threshold
	if snapshot.MaxDrawdown >= c.killSwitch {
		c.haltEntries("ceo_kill_switch", time.Now().UTC())
		c.log.Error("KILL SWITCH TRIGGERED — halting all trading",
			"drawdown", snapshot.MaxDrawdown,
			"threshold", c.killSwitch,
			"nav", snapshot.NAV,
		)
		return
	}

	// Cross-desk correlation check
	factors := c.checkCorrelation(snapshot, openPositions)
	c.recordPortfolioFactors(ctx, snapshot, factors)

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
		"top_factors", summarizeTopFactors(factors, 3),
	)
}

// checkCorrelation detects when too many desks lean the same direction.
func (c *CEO) checkCorrelation(snap book.PortfolioSnapshot, positions []*model.Position) []factorExposure {
	if snap.NAV <= 0 {
		return nil
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

	factors := c.factorExposures(snap.NAV, positions)
	policy := activeFactorPolicy()
	history := c.recentFactorHistory(context.Background(), policy)
	for _, factor := range factors {
		if factor.GrossPctNAV < policy.AlertGrossExposurePct &&
			math.Abs(factor.NetPctNAV) < policy.AlertNetExposurePct &&
			factor.DeskCount < policy.HiddenOverlapDeskCount {
			continue
		}
		c.log.Warn("factor concentration detected",
			"factor", factor.Factor,
			"gross_pct_nav", factor.GrossPctNAV,
			"net_pct_nav", factor.NetPctNAV,
			"desk_count", factor.DeskCount,
			"history_observations", history[factor.Factor].Observations,
			"history_avg_gross_pct_nav", history[factor.Factor].AverageGrossPctNAV,
			"desks", summarizeFactorDesks(factor),
		)
	}
	return factors
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

func (c *CEO) haltEntries(reason string, at time.Time) {
	if c == nil {
		return
	}
	c.halted = true
	if c.entryGate != nil {
		policy := c.entryGate.CurrentEntryPolicy()
		if policy.AllowEntries || policy.Reason != reason {
			c.entryGate.Disable(reason, at)
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
	penalties := c.crowdedFactorPenalties(snapshot.NAV, c.book.GetOpenPositions())
	policy := activeFactorPolicy()

	type deskPerf struct {
		id      string
		sharpe  float64
		weight  float64
		penalty float64
	}

	var perfs []deskPerf
	totalSharpe := 0.0

	for _, desk := range c.desks {
		returns := c.deskSharpe[desk.ID]
		s := rollingSharpeSafe(returns)
		// Floor: minimum 0.5 weight even for worst performers (prevents starvation)
		baseWeight := math.Max(0.5, s+1.0)
		penalty := penalties[desk.ID]
		w := math.Max(policy.MinWeightFloor, baseWeight*(1-penalty))
		perfs = append(perfs, deskPerf{id: desk.ID, sharpe: s, weight: w, penalty: penalty})
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
			"crowding_penalty", p.penalty,
			"new_capital", newCapital,
		)
	}
}

func summarizeTopFactors(factors []factorExposure, limit int) []string {
	if len(factors) == 0 || limit <= 0 {
		return nil
	}
	if len(factors) > limit {
		factors = factors[:limit]
	}
	summary := make([]string, 0, len(factors))
	for _, factor := range factors {
		summary = append(summary, factor.Factor+":"+formatFactorPct(factor.GrossPctNAV))
	}
	return summary
}

func summarizeFactorDesks(factor factorExposure) []string {
	desks := make([]string, 0, len(factor.DeskContributions))
	for deskID, contrib := range factor.DeskContributions {
		desks = append(desks, deskID+":"+formatFactorPct((contrib.Gross/maxFloat(factor.Gross, 1))*100))
	}
	sort.Strings(desks)
	return desks
}

func formatFactorPct(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64) + "%"
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
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

func (c *CEO) recordPortfolioFactors(ctx context.Context, snap book.PortfolioSnapshot, factors []factorExposure) {
	if c == nil || c.floor == nil || c.floor.graph == nil || len(factors) == 0 {
		return
	}

	observedAt := time.Now().UTC()
	portfolioID := c.portfolioGraphID()
	snapshotID := portfolioID + ":" + strconv.FormatInt(observedAt.UnixNano(), 10)
	graphSnapshot := model.PortfolioGraphSnapshot{
		ID:            snapshotID,
		PortfolioID:   portfolioID,
		SessionID:     strings.TrimSpace(c.floor.sessionID),
		NAV:           snap.NAV,
		Cash:          snap.Cash,
		GrossExposure: snap.GrossExposure,
		NetExposure:   snap.NetExposure,
		MaxDrawdown:   snap.MaxDrawdown,
		OpenPositions: snap.OpenPositions,
		ObservedAt:    observedAt,
		Factors:       factorSnapshots(factors),
	}
	if err := c.floor.graph.RecordPortfolioSnapshot(ctx, &graphSnapshot); err != nil {
		c.log.Warn("persist portfolio factor snapshot failed",
			"portfolio_id", portfolioID,
			"snapshot_id", snapshotID,
			"error", err,
		)
	}
}

func (c *CEO) portfolioGraphID() string {
	portfolioID := strings.TrimSpace(os.Getenv("PORTFOLIO_GRAPH_ID"))
	if portfolioID == "" {
		portfolioID = "primary"
	}
	return portfolioID
}
