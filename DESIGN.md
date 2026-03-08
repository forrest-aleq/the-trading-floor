# The Trading Floor

## Autonomous Alpha Generation System — Canonical Architecture

---

## Executive Summary

An autonomous trading system built on the MARS cognitive architecture, operating as a virtual trading firm — not a flat swarm of agents, but an organizational hierarchy with desks, roles, authority, capital allocation, and accountability.

**Core thesis**: Alpha exists in the synthesis of information that no single human or institution can hold simultaneously. Not because the information is secret, but because there's too much of it, in too many languages, across too many sources, for any human to correlate. The cost of intelligence has collapsed. This system applies that intelligence correctly, continuously, and faster than any prior market participant.

The system runs 24/7 against real market data via Interactive Brokers paper trading. 40 independent desks, each allocated $25,000 from a $1,000,000 paper trading account. 20 desks run the full MARS belief architecture. 20 run identical LLM reasoning without the belief system. The A/B test proves whether accumulated institutional memory — beliefs, engrams, earned autonomy — generates alpha beyond raw LLM reasoning.

Target: 2% daily return on aggregate across all desks. Not every desk every day — but the portfolio-level equity curve trending upward consistently over 30 days of forward testing against live market data.

Built in Go. Single binary. Runs forever.

**Broker**: Interactive Brokers (IBKR Pro). 170 markets across 40 countries — stocks, options, futures, forex, bonds, funds. Native Go client via `scmhub/ibapi`. Paper trading on identical API (port 4002 vs 4001 live). 90+ order types and algos. This is the only broker that matches "every financially tradeable instrument in existence."

**Infrastructure**: $135k Azure credits, ~115 days. Self-hosted Qwen 72B/7B on Azure GPUs for unlimited 24/7 inference (~$14k/month). Claude Sonnet API for critical decisions only (~5% of calls). Total monthly burn ~$15k. Credits last well beyond the test window.

---

## Philosophical Foundation

### Why This Should Work

Renaissance Technologies made 66% annually with human quants, proprietary hardware, and rigid statistical models that took years to develop. Their constraint was the cost and speed of intelligence.

This system runs intelligence at near-zero marginal cost. A self-hosted Qwen 72B instance processes and understands a 10-K filing in seconds. A human analyst takes a week. Renaissance had ~300 employees at peak. This system runs hundreds of agents 24/7 for the cost of GPU electricity. These agents don't just do statistical pattern matching — they read, reason, argue, form theses, challenge each other, and synthesize across languages and source types.

No one has run 40 autonomous desks with LLM-powered reasoning across every instrument class 24/7. There is no benchmark for this because it hasn't existed before. The 115-day paper trading test exists to establish that benchmark.

### Core Principles

**Desks, not swarms.** A swarm is good at search. A team is good at judgment. The unit of intelligence is the permanent desk — a trio of specialists that compounds shared experience over time.

**Seats, not models.** The belief graph attaches to the seat (trader, analyst, researcher), not the model vendor occupying it. Specialization becomes institutional memory, not model temperament. A seat can be occupied by Claude, Qwen, Gemini — the accumulated beliefs persist regardless.

**Local belief depth, not universal flooding.** A specialist analyst with a deep, scarred belief graph in one domain is more valuable than a generic super-agent with shallow access to everything. The edge comes from repeated exposure and accumulated taste.

**Triangulation through structured disagreement.** Groups work independently, form views, then collide. Two independent groups building the same wrong narrative from different starting points is astronomically unlikely. If they converge independently, that's signal. If they diverge, the divergence localizes uncertainty.

**Overfitting as a controlled tool.** Permanent desks stay general within their domain — natural regularization through volume and variety. Temporary sub-teams overfit intentionally to specific situations, report upward, and dissolve. The desk discounts sub-team findings using its broader experience. Surgical depth wrapped in general wisdom.

### Theoretical Foundations

**Information Cascade Theory.** Markets misprice when participants decide based on what others do rather than private information. The system reads underlying information directly and forms independent beliefs.

**Reflexivity (Soros).** Markets influence reality in circular loops. War → oil → inflation → rates → equities → confidence → economy → more market movement. The system traces reflexive loops faster and more completely than humans.

**Granger Causality.** The system looks for predictive information flow, not correlation. A Farsi Telegram group discussing troop movements Granger-causes a shift in oil futures 6 hours later.

**Efficient Market Hypothesis failure.** EMH assumes all information is instantly priced. But "all information" requires someone reading a Telegram group in Arabic about Strait of Hormuz shipping AND connecting it to energy options AND doing it at 3am. Nobody does that. This system does.

**Complex Adaptive Systems.** Simple agents following simple rules, interacting with each other and their environment, produce emergent intelligence that no single agent possesses. No single agent sees the whole picture. The synthesis layer sees something no human analyst, hedge fund, or intelligence agency sees.

---

## The Firm: Organizational Architecture

### Overview

```
CEO Referee (capital allocation, rule enforcement, correlation monitoring)
  |
  |-- Desk 1: Geopolitical (Trio: Trader + Analyst + Researcher)
  |     |-- [spawns] Sub-team: Strait of Hormuz deep-dive (3-6 temp agents)
  |     |-- [spawns] Sub-team: Sanctions impact analysis (3-6 temp agents)
  |
  |-- Desk 2: Macro-Economic (Trio: Trader + Analyst + Researcher)
  |     |-- [spawns] Sub-team: Fed policy cascade (3-6 temp agents)
  |
  |-- Desk 3: Corporate Alpha (Trio: Trader + Analyst + Researcher)
  |     |-- [spawns] Sub-team: EDGAR filing anomaly (3-6 temp agents)
  |
  |-- ... (40 desks total)
  |
  |-- The Wire (shared signal ingestion utility)
  |-- The Book (portfolio-level position tracking)
  |-- The Risk System (hard rule enforcement)
  |-- The Memory (belief graphs, engrams, episodic storage)
```

### The CEO Referee

One agent. Does not trade. Does not research. Does not scan.

**What it does:**

* **Capital reallocation** on a weekly/biweekly schedule based on risk-adjusted performance (Sharpe ratio, win rate, drawdown, thesis quality — not raw P&L alone)
* **Rule enforcement** in real time: daily loss limits, position concentration limits, correlation limits. Hard rules enforced by the risk system, triggered by the CEO's monitoring
* **Cross-desk correlation monitoring**: detects when the firm is unintentionally concentrated in one direction across multiple desks. Forces diversification or hedging when portfolio-level exposure exceeds thresholds
* **Desk creation and destruction**: spawns new desks in productive domains, shuts down desks in dead domains, reallocates capital
* **Performance forensics**: when a desk loses money, examines belief graph quality, thesis reasoning, prosecution effectiveness, group dynamics. Findings inform future desk construction
* **Meta-skeptic function**: watches for correlated confidence across desks. When all desks lean the same direction, asks "are these views actually independent or downstream of the same data source?" Has authority to force all desks into reasoning mode during regime transitions

**What it does NOT do:**

* Approve or reject individual trades
* Override desk-level thesis formation
* Route signals to specific desks
* Manage individual positions

**The CEO's belief graph** operates one level of abstraction above trading beliefs. It holds meta-beliefs: "Energy desk in high-vol regimes produces Sharpe 2.1." "Macro desk in trending regimes produces Sharpe 1.4 but with lower drawdown." "Tail desk loses money 11 months out of 12 but the 12th month pays for everything." These meta-beliefs drive capital allocation.

### The Desks

Each desk is a permanent group of three agents: Trader, Analyst, Researcher. They cover a domain. They persist indefinitely. Their group belief graph compounds over months.

**The Researcher** expands the search space. Ingests signals, follows threads, gathers evidence, finds non-obvious connections. Lives closest to the raw information.

**The Analyst** compresses the search space into structure. Contextualizes findings against history, models cascades, calculates base rates, forms theses with conviction scores. Also responsible for challenging whether current beliefs were earned in conditions that still hold (regime awareness).

**The Trader** forces contact with execution reality. Selects the optimal instrument and trade structure, evaluates liquidity and timing, manages position sizing, monitors open positions against kill conditions. Translates thesis into the most asymmetric market expression.

These roles naturally pull against each other. The researcher wants to explore. The analyst wants to be right. The trader wants to manage risk. That tension is productive.

**Group dynamics and belief:**

* The trio works their own angles before comparing notes on each signal
* The group develops shared episodic memory — "last time we saw this pattern, here's what happened"
* Group-level engrams form: compressed plans that include the group's reasoning process, not just the trade outcome
* The belief graph tracks group cognitive performance: "did this group's reasoning process, applied to this type of signal, in this market regime, produce alpha?"
* The group learns how much to trust its own sub-teams' findings over hundreds of episodes

### Desk Organization: By Information Type, Not Asset Class

The same current event produces trades across multiple asset classes. Organizing by information type produces naturally diversified, uncorrelated trade ideas from the same inputs.

**40 desks = ~8 domains x ~5 competing approaches per domain**

Within each domain, multiple desks run different analytical philosophies against the same signal stream. After 8 weeks, the CEO referee knows which approach actually works in current conditions.

#### Domain 1: Geopolitical (4-5 desks)

Iran war, China-Taiwan, European elections, sanctions, military conflicts, diplomatic shifts. Trades energy, defense, currencies, commodities, country ETFs.

* Desk A: Supply-chain cascade focus, options structures, medium timeframe
* Desk B: Political outcome focus, event-driven binary bets, short timeframe
* Desk C: Second-order economic effects, equity sectors, longer timeframe
* Desk D: Defense/security sector specialist, single-name options

#### Domain 2: Macro-Economic (4-5 desks)

Fed decisions, inflation, employment, GDP, global central banks. Trades rates, indices, sector rotation, forex, vol products.

* Desk A: Rate-sensitive instruments, options around FOMC
* Desk B: Cross-asset macro, futures and forex
* Desk C: Inflation/deflation thesis, commodity-equity correlation
* Desk D: Yield curve and credit spread focus

#### Domain 3: Corporate (4-5 desks)

Earnings, filings, M&A, management changes, regulatory actions. Trades single-name equities and options.

* Desk A: Earnings event specialist, straddles and vol plays
* Desk B: Filing anomaly detection (10-K footnotes, Form 4 patterns)
* Desk C: M&A arbitrage and special situations
* Desk D: Quality/value fundamental, longer-duration

#### Domain 4: Flows and Sentiment (4-5 desks)

Options flow, dark pool prints, Reddit/Twitter sentiment, positioning data, short interest. The contrarian desks.

* Desk A: Options flow anomaly detection, follow smart money
* Desk B: Sentiment extreme contrarian, mean-reversion
* Desk C: Gamma/positioning squeeze detection
* Desk D: Cross-asset flow divergence

#### Domain 5: Tail Risk (3-4 desks)

Black swans, systemic stress, things that almost never happen. Permanently skeptical. Loses money most months. Fixed budget from CEO regardless of recent performance.

* Desk A: Geopolitical tail (war escalation, nuclear, pandemic)
* Desk B: Financial system tail (credit freeze, bank runs, liquidity crisis)
* Desk C: Market structure tail (flash crash, circuit breaker, clearing failure)

#### Domain 6: Volatility (4-5 desks)

Pure vol trading. IV vs RV, term structure, skew, vol-of-vol.

* Desk A: Variance risk premium harvesting (sell premium)
* Desk B: Vol event trading (buy vol before catalysts)
* Desk C: Term structure and calendar spreads
* Desk D: Cross-asset vol correlation

#### Domain 7: Sector Specialist (4-5 desks)

Deep single-sector knowledge across tech, biotech, energy, financials.

* Desk A: Tech mega-cap specialist
* Desk B: Biotech/FDA catalyst specialist
* Desk C: Energy sector specialist (upstream, midstream, downstream)
* Desk D: Financials/bank specialist

#### Domain 8: Systematic (3-4 desks)

Quantitative, rules-based strategies that complement the discretionary desks.

* Desk A: Momentum/trend following
* Desk B: Mean reversion
* Desk C: Statistical arbitrage / pairs trading
* Desk D: Options premium selling (theta harvesting)

### Temporary Sub-Teams

When a desk identifies something worth going deep on, it spawns a focused sub-team.

**Spawn conditions:**

* Desk identifies a critical unknown that requires deeper investigation
* Multiple signals converge on a theme that needs synthesis across more sources than the trio can handle
* A time-sensitive opportunity requires parallel deep-dive across multiple data streams

**Sub-team characteristics:**

* 3-12 agents, spawned on demand
* Focused on one specific question
* Time-bounded (typically 24-72 hours)
* Inherit the parent desk's context (they know WHY they're researching)
* Backfilled with domain-specific historical beliefs on spawn
* Report findings upward to the desk in compressed, structured format
* Do NOT carry permanent beliefs — their findings get absorbed into the desk's belief graph
* Dissolve when the question is answered or the time bound expires

**Sub-teams do NOT become belief-bearing citizens.** This is critical anti-overfitting architecture. Temporary agents do work, produce findings, and dissolve. Only findings that the desk validates through repeated observation become durable engrams. Organizational regularization.

---

## The MARS Belief Architecture: Trading Adaptation

The MARS cognitive architecture provides the brain. This section specifies how each MARS component maps to the trading floor.

### The Cognitive Loop

```
1. THINK    — Signal arrives. Load desk state, get desk competence beliefs.
              "Do I have experience with this type of signal in this regime?"

2. REMEMBER — Check engram library (global proven patterns + desk-specific patterns).
              "Have I traded this exact pattern before? What happened?"

3. PLAN     — Gap detector identifies unknowns. Research desk forms thesis.
              Analyst maps cascade. Trader selects instrument and structure.
              "What's the trade?"

4. CHECK    — Risk desk validates against pack constraints.
              Position limits, exposure, correlation, drawdown, daily loss.
              "Am I allowed to take this trade?"

5. ROUTE    — Select strategy archetype and trade structure based on
              belief confidence, risk tier, and capital allocation.
              "How should I express this view?"

6. ACT      — Submit order to Interactive Brokers via API.
              "Execute."

7. LEARN    — Trade resolves. Update desk beliefs, group engrams.
              Record full episode. Feed CEO meta-beliefs.
              "What did I learn?"
```

### Trading Pack Structure

```
trading-pack/
  ontology.json       — instruments, sectors, asset classes, relationships,
                        cascade paths between domains
  capabilities.json   — trade structures organized hierarchically with risk tiers
  policies.json       — risk constraints (deterministic, no LLM)
  objectives.json     — performance targets (Sharpe, win rate, drawdown, edge capture)
```

#### Capability Hierarchy (capabilities.json)

```
Trading Pack
  |-- Equity
  |     |-- equity.long              (risk tier 2)
  |     |-- equity.short             (risk tier 3)
  |     |-- equity.pairs             (risk tier 2)
  |
  |-- Options
  |     |-- options.long_call        (risk tier 2)
  |     |-- options.long_put         (risk tier 2)
  |     |-- options.covered_call     (risk tier 1)
  |     |-- options.cash_secured_put (risk tier 2)
  |     |-- options.bull_call_spread (risk tier 2)
  |     |-- options.bear_put_spread  (risk tier 2)
  |     |-- options.iron_condor      (risk tier 2)
  |     |-- options.straddle         (risk tier 3)
  |     |-- options.strangle         (risk tier 3)
  |     |-- options.calendar_spread  (risk tier 2)
  |     |-- options.ratio_spread     (risk tier 4)
  |     |-- options.naked_put        (risk tier 4)
  |     |-- options.naked_call       (risk tier 5)
  |     |-- options.butterfly        (risk tier 2)
  |
  |-- Macro
  |     |-- macro.futures_long       (risk tier 3)
  |     |-- macro.futures_short      (risk tier 3)
  |     |-- macro.futures_spread     (risk tier 2)
  |     |-- macro.forex              (risk tier 3)
  |
  |-- Tail
        |-- tail.far_otm_puts       (risk tier 2 — small premium, defined risk)
        |-- tail.far_otm_calls      (risk tier 2)
        |-- tail.straddle_basket    (risk tier 3)
```

**Belief cascade paths:** Trust earned at `options.covered_call` partially cascades to `options.cash_secured_put` (similar mechanics). Does NOT cascade to `macro.futures_long` (different risk domain). Cascade paths defined in ontology.

#### Risk Policies (policies.json)

All deterministic. No LLM. Pass or block.

```json
{
  "per_desk": {
    "max_daily_loss_pct": 3.0,
    "max_single_position_pct": 20.0,
    "max_correlated_positions": 3,
    "max_open_positions": 10,
    "halt_on_daily_loss": true
  },
  "per_trade": {
    "max_position_size_pct": 10.0,
    "min_conviction_score": 0.65,
    "min_prosecution_survival": true,
    "required_kill_conditions": true
  },
  "portfolio_level": {
    "max_single_factor_exposure_pct": 25.0,
    "max_total_drawdown_pct": 10.0,
    "regime_shift_forces_reasoning_mode": true,
    "kill_switch_drawdown_pct": 15.0
  }
}
```

### Belief System Adaptations for Trading

#### Magnitude-Weighted Outcomes

In MARS, outcomes are binary (success/failure). In trading, magnitude matters. A $50 win and a $5,000 win are both successes with different belief implications.

```
delta(o) = clip(realized_return / expected_risk, -2.0, +2.0)
```

* Trade returns 3x risk taken → strong positive signal
* Trade loses 0.5x risk → mild negative signal
* Trade loses 2x risk (gap through stop) → severe negative signal
* Boundary violation (blew through risk limit) → 10x moral asymmetry multiplier

#### Regime-Conditional Beliefs

The same strategy in different market regimes must have SEPARATE beliefs.

```
Key: (desk, capability, context, regime)

Example:
  ("energy-desk-a", "options.straddle", "earnings_play", "low_vol")  → trust: 0.82
  ("energy-desk-a", "options.straddle", "earnings_play", "high_vol") → trust: 0.54
```

NOT a blended score. Two separate entries gated by regime context. The gap detector flags when current regime doesn't match the regime where belief was earned.

**Regime detection dimensions:**

* Volatility regime: VIX level (low < 15, medium 15-25, high > 25, extreme > 35)
* Trend regime: trending vs mean-reverting (measured by Hurst exponent or ADX)
* Risk regime: risk-on vs risk-off (measured by credit spreads, safe haven flows)
* Liquidity regime: normal vs stressed (measured by bid-ask spreads, market depth)

#### Trust Decay During Regime Transitions

When the system detects a regime shift, ALL autonomy modes across ALL desks drop one level until beliefs are re-validated in the new regime. The CEO referee triggers this.

* AUTONOMOUS → REASONING for all capabilities affected by the regime dimension that shifted
* Must re-earn autonomy through validated performance in the new regime
* Minimum 20 successful trades in new regime before autonomy can be restored

This prevents the single most dangerous failure mode: a system that earned autonomous mode in a bull market confidently executing a playbook that's wrong in a crash.

#### Episode-Based Learning (Not Mark-to-Market)

Trading has open positions. The belief system updates on CLOSED episodes, not intermediate marks.

```
Episode lifecycle:
  Entry → Position Open → [mark-to-market monitoring, no belief update]
  → Exit Trigger → Position Closed → Episode Complete → Belief Update
```

The gap detector watches open positions for:

* **unsafe**: thesis invalidated by new information → close immediately
* **stale**: position held longer than thesis timeframe → reassess
* **blocking**: can't exit (no liquidity, market halted) → flag for desk attention

#### Correlation Between Beliefs

Beliefs propagate not just hierarchically (within pack tree) but laterally (across correlated instruments and desks).

If a desk loses on 5 consecutive oil-related trades:

* Negative signal for specific oil capabilities
* Attenuated negative signal for the entire energy sector thesis
* Attenuated negative signal for the macro archetype that generated those trades
* Informational signal for Wire sources that produced those signals

Neo4j models these relationships as graph edges. Belief updates propagate through them with configurable attenuation.

#### Adversarial Market Error Classification

| Category | Description | Belief Update |
|----------|-------------|---------------|
| `thesis_failure` | Thesis was wrong, market moved against | Full negative update |
| `execution_friction` | Thesis correct but slippage/fills degraded return | Attenuated negative |
| `infrastructure_error` | Broker API, network, timeout | Skip — no update |
| `policy_block` | Risk system blocked the trade | Record as policy event |
| `market_halt` | Circuit breaker, trading halt | Skip — no update |

### Gap Detector: Trading Adaptation

| Category | Trading Translation | Priority |
|----------|-------------------|----------|
| **unsafe** | Flash crash detected, extreme correlation breakdown, liquidity evaporation, kill switch conditions, thesis invalidated for open position | 1 (highest) |
| **blocking** | Broker API down, market halted, position limit reached, can't exit position | 2 |
| **unknown** | No base rate for this contract type, IV data stale, earnings date unconfirmed, regime classification uncertain | 3-5 |
| **stale** | Market regime shifted since beliefs earned, strategy unvalidated in current conditions, model version changed, position held past thesis timeframe | 7-8 |

### Engrams: Trading Patterns

**Global engram (Layer 1 — cross-desk):**

```json
{
  "intent_key": "earnings_straddle_low_iv",
  "context_pattern": "iv_percentile_lt_20 AND earnings_in_14d AND hist_move_gt_implied",
  "capability": "options.straddle",
  "success_count": 73,
  "failure_count": 39,
  "avg_return": 0.14,
  "sharpe": 1.8,
  "regime_tags": ["low_vol", "bull"],
  "min_desks_observed": 6
}
```

**Desk engram (Layer 2 — desk-specific):**

```json
{
  "intent_key": "earnings_straddle_low_iv_tech_megacap",
  "context_pattern": "iv_percentile_lt_20 AND earnings_in_14d AND sector_tech AND market_cap_gt_100B",
  "capability": "options.straddle",
  "success_count": 28,
  "failure_count": 9,
  "avg_return": 0.19,
  "optimal_dte": "10-14",
  "desk_id": "tech-desk-a",
  "regime_tags": ["low_vol"]
}
```

New desks get Layer 1 engrams (the playbook) but zero trust (haven't proven they can execute). Plans available, autonomy not. Exactly as MARS designed.

### Adjudication: Trading Version

| Tier | Source | When Used | Cost |
|------|--------|-----------|------|
| 1 | Execution status | Order filled, position opened/closed | Free |
| 2 | P&L calculation | Trade resolved, realized return computed | Free |
| 3 | Thesis validation | Did the thesis play out? (LLM evaluates original thesis against what actually happened) | Medium |
| 4 | Desk review | Desk trio evaluates trade quality holistically | Expensive |

Low-conviction autonomous trades: Tier 1+2 only.
High-conviction thesis-driven trades: Tier 1+2+3.
Novel or large positions: Tier 1+2+3+4.

---

## The Seven Pillars

### Pillar 1: The Wire (Signal Ingestion)

Shared utility. Every desk sees every signal. Each desk filters through its own lens.

```
internal/wire/
├── manager.go          # Central signal bus, fan-out to subscribers
├── feeds/
│   ├── news.go         # RSS/Atom feeds (50+ sources, tiered polling from AiFW)
│   ├── sec.go          # SEC EDGAR RSS, full-text filing fetch
│   ├── social.go       # Twitter/X, Reddit, StockTwits
│   ├── telegram.go     # Telegram group monitoring (geopolitical signals)
│   ├── macro.go        # Fed, Treasury, ECB, BOJ, economic calendars
│   ├── market.go       # IBKR real-time market data streaming (TWS API)
│   ├── earnings.go     # Earnings call transcripts, guidance extraction
│   └── alt.go          # Job postings, shipping data, satellite imagery APIs
├── translate.go        # LLM translation (Qwen 7B, any language → English)
├── normalize.go        # Signal normalization to common schema
├── dedup.go            # Content hash + semantic similarity (threshold 0.92)
└── embed.go            # Vector embedding for every signal (pgvector)
```

#### Signal Schema

```go
type Signal struct {
    ID         string          `json:"id"`
    Source     string          `json:"source"`
    Type       SignalType      `json:"type"`       // news, price, economic, filing, social, alternative
    Category   string          `json:"category"`   // geopolitical, macro, corporate, flow, etc.
    Timestamp  time.Time       `json:"timestamp"`
    Urgency    float64         `json:"urgency"`    // 0.0-1.0
    Strength   float64         `json:"strength"`   // 0.0-1.0
    Direction  *Direction      `json:"direction"`  // bullish, bearish, neutral, or nil
    Entities   []Entity        `json:"entities"`   // companies, people, instruments, countries
    Languages  []string        `json:"languages"`  // original languages
    Raw        json.RawMessage `json:"raw"`        // original payload
    Translated string          `json:"translated"` // English translation if non-English
    Embedding  []float32       `json:"embedding"`  // vector embedding for clustering
}
```

#### Signal Volume Targets

| Source | Signals/day | Latency Target |
|--------|------------|----------------|
| News RSS | 5,000-10,000 | <30s from publish |
| SEC EDGAR | 500-2,000 | <60s from filing |
| Social media | 50,000-100,000 | <15s from post |
| Telegram | 10,000-50,000 | <10s from message |
| Market data | Continuous stream | <1ms (IBKR TWS native) |
| Macro releases | 50-200 | <5s from release |

#### Deduplication

At 100k+ signals/day, the same Reuters headline shows up from 30 different RSS feeds. Without dedup, this triggers 30 parallel research processes on the same thesis and creates false signal strength ("47 sources confirm this" when it's really 1 source echoed 47 times).

Two layers:
1. **Content hash** (SHA-256 of normalized text) — catches exact duplicates
2. **Semantic similarity** (cosine distance of embeddings, threshold 0.92) — catches paraphrases

Dedup runs in the Wire before fan-out. Downstream desks never see duplicates.

#### Backpressure

When processing can't keep up with signal volume, signals queue in buffered Go channels rather than drop. A dropped signal could mean a missed opportunity.

```go
// Wire fan-out with backpressure
signalCh := make(chan Signal, 10000) // Buffer 10k signals

// If buffer fills, log warning but don't block producers
select {
case signalCh <- sig:
default:
    metrics.SignalDropped(sig.Source)
    // Still queue to overflow (Redis stream) for eventual processing
    overflow.Enqueue(sig)
}
```

#### Multi-Language Processing

Self-hosted Qwen 7B handles translation. Critical for:

* Farsi-language shipping/military channels
* Arabic-language geopolitical intelligence
* Chinese economic data and policy signals
* Japanese central bank communications
* Turkish, Russian regional intelligence

Translation is a Wire function, not a desk function. Desks receive translated signals. The original language is preserved for provenance.

### Pillar 2: The Scanner (Opportunity Detection)

Filter the firehose. 99% of signals are noise. Find the 1% that are tradeable.

```
internal/scanner/
├── engine.go           # Parallel scanner orchestration
├── filters/
│   ├── momentum.go     # Price/volume breakouts, unusual activity
│   ├── volatility.go   # Vol surface changes, term structure shifts
│   ├── event.go        # Material events (8-K, earnings, M&A, lawsuits)
│   ├── anomaly.go      # Statistical anomalies vs historical baseline
│   ├── macro.go        # Macro regime changes, policy shifts
│   ├── sentiment.go    # Sentiment spikes, narrative shifts
│   ├── correlation.go  # Cross-asset correlation breaks
│   └── cascade.go      # Information cascade detection (Soros reflexivity)
└── scorer.go           # Opportunity scoring (0-100), threshold gating
```

#### The Cascade Detector

This is the alpha. The system's unique edge.

```go
// CascadeDetector finds when information is flowing between
// seemingly unrelated domains — the Soros reflexivity engine.
//
// Example: Iran conflict → shipping route disruption signals on
// Telegram → oil futures move → defense stocks move → but
// cybersecurity stocks haven't moved yet → TRADE THIS GAP
//
// The gap between "information exists" and "market prices it"
// is where we live.
type CascadeDetector struct {
    graph      *knowledge.Graph  // Neo4j entity/relationship graph
    embeddings *embedding.Store  // pgvector for semantic clustering
    window     time.Duration     // Correlation window (default: 48h)
    threshold  float64           // Min correlation strength (default: 0.7)
}

func (c *CascadeDetector) Detect(ctx context.Context, signals []Signal) []Cascade {
    // 1. Cluster signals by embedding similarity
    // 2. Find clusters spanning multiple unrelated sources
    // 3. Trace information flow through entity graph
    // 4. Identify assets that SHOULD have moved but HAVEN'T
    // 5. Score the gap (size x confidence x time_pressure)
}
```

The cascade detector requires both vector embeddings (to cluster semantically similar signals across languages and sources) and the Neo4j entity graph (to trace causal/relational paths between entities). Neither alone is sufficient.

### Pillar 3: The Research Desk (Thesis Formation)

Take an opportunity and build a rigorous, prosecuted thesis.

```
internal/research/
├── desk.go             # Research orchestration (trio conversation)
├── strategies/
│   ├── scalper.go      # Sub-minute to hours, momentum/mean-reversion
│   ├── event.go        # Event-driven (earnings, M&A, regulatory)
│   ├── macro.go        # Macro regime trades (rates, FX, commodities)
│   ├── fundamental.go  # Deep fundamental analysis (10-K parsing, DCF)
│   ├── contrarian.go   # Crowd-is-wrong trades, sentiment extremes
│   └── tail.go         # Black swan / fat tail positioning
├── thesis.go           # Thesis data structure + lifecycle
├── prosecutor.go       # Adversarial challenge (tries to kill the thesis)
├── council.go          # Multi-archetype debate for large positions
├── evidence.go         # Evidence gathering and weighting
└── antiportfolio.go    # Tracks rejected theses and their counterfactual outcomes
```

#### The Thesis

```go
type Thesis struct {
    ID            string
    Opportunity   *Opportunity
    Strategy      StrategyCategory
    Direction     Direction
    Instrument    Instrument

    // Conviction
    Conviction    float64           // 0-1, must exceed threshold to trade
    Evidence      []Evidence        // Supporting evidence chain
    CounterArgs   []CounterArgument // From the Prosecutor

    // Health — continuous degradation signal
    Health        float64           // 0-1, degrades when evidence weakens
                                    // At 0.85: full position
                                    // At 0.5:  reduce by 50%
                                    // At 0.3:  close position

    // Trade Parameters
    Entry         PriceLevel
    Target        PriceLevel
    StopLoss      PriceLevel
    PositionSize  float64           // Determined by Kelly criterion + risk limits
    TimeHorizon   time.Duration

    // Kill Conditions
    KillRules     []KillRule        // Auto-exit conditions
    MaxLoss       float64           // Absolute max loss
    Expiry        time.Time         // Thesis expires if not triggered

    // Lifecycle
    Status        ThesisStatus      // Embryo → Nursery → Prosecuted → Active → Resolved
    CreatedAt     time.Time
    ResolvedAt    *time.Time
    Outcome       *ThesisOutcome    // Win/Loss/Expired + P&L
}
```

**Thesis Health** is a continuous score, not a binary flag. It degrades when supporting evidence weakens — if three of five evidence pillars get contradicted, health drops from 0.85 to 0.4. This creates a continuous signal for position management:

* Health > 0.7: maintain full position
* Health 0.5-0.7: reduce position by (1 - health) * 100%
* Health 0.3-0.5: close 75% of position
* Health < 0.3: close entirely

The gap detector feeds health: `stale` degrades slowly, `unsafe` degrades immediately.

#### The Prosecutor

```go
// The Prosecutor tries to KILL every thesis before it can trade.
// If the thesis survives prosecution, conviction is real.
type Prosecutor struct {
    llm llm.Client // Claude Sonnet — this is a critical decision
}

func (p *Prosecutor) Prosecute(ctx context.Context, thesis *Thesis) *Prosecution {
    // 1. Generate 5-10 bear arguments against the thesis
    // 2. Search for contradicting evidence
    // 3. Find historical analogues where similar trades failed
    // 4. Check for crowded positioning (if everyone sees it, it's priced in)
    // 5. Stress test assumptions (what if X is wrong?)
    // 6. Rate thesis survival: KILLED, WEAKENED, SURVIVED, STRENGTHENED
}
```

#### The Council

Different from prosecution. Prosecution asks "why is this wrong?" The Council asks "how does this look from five different strategic perspectives simultaneously?"

```go
// Convened for positions requesting >2% of portfolio.
// Multiple strategy archetypes evaluate from their perspective.
type Council struct {
    archetypes []StrategyArchetype
    llm        llm.Client
}

func (c *Council) Debate(ctx context.Context, thesis *Thesis) *Verdict {
    perspectives := make([]Perspective, len(c.archetypes))
    for i, arch := range c.archetypes {
        // Each archetype evaluates independently
        // Fundamental: "Do the numbers support this?"
        // Contrarian: "Is this already crowded?"
        // Macro: "Does the regime support this?"
        // Tail: "What's the worst case scenario?"
        // Scalper: "Is the timing right?"
        perspectives[i] = arch.Evaluate(ctx, thesis)
    }

    // Convergence = high conviction. Divergence = localized uncertainty.
    // Output: conviction adjustment, position size recommendation
    return c.synthesize(perspectives)
}
```

#### The Anti-Portfolio

Every thesis killed by the prosecutor or blocked by risk gets saved with a full snapshot. Then retroactively, the system checks: "what would have happened if we'd taken that trade?"

```go
type AntiPortfolioEntry struct {
    ID               string
    ThesisSnapshot   *Thesis          // Full thesis at time of rejection
    RejectionReason  string           // "prosecution_killed", "risk_blocked", "low_conviction"
    RejectedAt       time.Time
    WouldHavePnL     *float64         // Calculated retroactively
    WouldHaveOutcome *ThesisOutcome   // What actually happened in the market
}
```

**Meta-learning about conservatism:**
* If the anti-portfolio is consistently profitable → prosecutor is killing good trades → loosen prosecution threshold
* If the anti-portfolio is consistently unprofitable → prosecutor is doing its job → maintain or tighten
* Tracked per desk and per strategy archetype
* Prevents the system from becoming so cautious it never trades

### Pillar 4: The Risk Desk (Pre-Trade Validation)

Deterministic, hard-coded risk limits. No AI. No exceptions. No override.

```
internal/risk/
├── gate.go             # Central risk gate — ALL orders pass through
├── limits/
│   ├── position.go     # Max position size per instrument, sector, asset class
│   ├── portfolio.go    # Max portfolio heat, gross/net exposure
│   ├── drawdown.go     # Daily/weekly/monthly drawdown limits
│   ├── correlation.go  # Max correlated exposure
│   ├── greeks.go       # Options greeks limits (delta, gamma, vega, theta)
│   └── concentration.go # Max concentration per name/sector/geography
├── killswitch.go       # Emergency halt — kills ALL trading
├── tokens.go           # Capability token minting and validation
└── circuit_breaker.go  # Per-strategy circuit breakers
```

#### Hard Rules

```
Per Trade:
  - Position size <= X% of desk capital
  - Valid capability token required
  - Conviction score >= minimum threshold
  - Kill conditions defined
  - Prosecution survived

Per Desk:
  - Daily loss <= 3% of desk capital → auto-halt for day
  - Max open positions <= 10
  - Max correlated positions <= 3
  - Max single position <= 20% of desk capital

Per Portfolio:
  - Total drawdown <= 10% of portfolio → CEO forces regime assessment
  - Total drawdown <= 15% of portfolio → KILL SWITCH — halt everything
  - Single factor exposure <= 25% of portfolio
  - Regime shift detected → all desks drop to reasoning mode
```

#### Capability Tokens

```json
{
  "capability": "options.iron_condor",
  "constraints": {
    "max_contracts": 10,
    "max_premium_risk": 500,
    "underlying": "SPY",
    "expiry_range": "7-45_DTE"
  },
  "desk_id": "vol-desk-a",
  "expiry": "2026-03-08T16:00:00Z",
  "nonce": "a8f3...",
  "signature": "hmac-sha256(...)"
}
```

The risk system rejects any order without a valid, unexpired, correctly-scoped token. Even if a research agent hallucinates a conviction score, the token constrains what can actually happen.

#### Kill Switch

Three trigger conditions:

1. **Portfolio drawdown** exceeds 15% → halt all trading, close all positions, CEO reconvenes desks
2. **System anomaly** detected (correlated failures across desks, data feed corruption, broker API instability) → halt and diagnose
3. **Manual trigger** — human operator can halt at any time

### Pillar 5: The Execution Desk (Order Management)

Execute trades through Interactive Brokers with institutional-grade order management.

```
internal/execution/
├── manager.go          # Order lifecycle state machine
├── ibkr/
│   ├── client.go       # Wraps scmhub/ibapi Go library (TWS API socket)
│   ├── orders.go       # Order type builders (market, limit, stop, adaptive, TWAP, VWAP)
│   ├── market_data.go  # Real-time streaming via TWS API
│   ├── contracts.go    # Contract lookup and resolution (170 markets, 40 countries)
│   ├── account.go      # Position/balance queries
│   └── connection.go   # Connection management, auto-reconnect, heartbeat
├── router.go           # Smart order routing by size and instrument
├── slicer.go           # Large order slicing (iceberg, TWAP)
├── saga.go             # Multi-leg order saga orchestration
└── fill_tracker.go     # Fill tracking, partial fill handling
```

#### Interactive Brokers Integration

| Feature | Implementation |
|---------|---------------|
| Connection | TWS API socket via `scmhub/ibapi` — port 4002 (paper) / 4001 (live) |
| Order types | Market, Limit, Stop, StopLimit, Adaptive, TWAP, VWAP, Relative, MidPrice |
| Market data | Real-time streaming bars, quotes, depth of book |
| Instruments | Stocks, Options, Futures, Forex, Bonds, Funds — 170 markets, 40 countries |
| Paper trading | Identical API, different port, $1M virtual capital |
| Account type | IBKR Pro (required for API access) |
| Go client | `scmhub/ibapi` (updated Feb 2026, mirrors official Python/C++ API) |

#### Smart Order Routing

```go
func (r *Router) Route(order Order, contract ibkr.Contract) ibkr.Order {
    switch {
    case order.Notional < 10000:
        // Small orders: market or limit, direct
        return ibkr.LimitOrder(order.Direction, order.Quantity, order.LimitPrice)
    case order.Notional < 100000:
        // Medium orders: adaptive algo for price improvement
        return ibkr.AdaptiveOrder(order.Direction, order.Quantity, order.LimitPrice)
    default:
        // Large orders: TWAP over 15-30 minutes to minimize impact
        return ibkr.TWAPOrder(order.Direction, order.Quantity, 15*time.Minute)
    }

    // Options always use MidPrice for price improvement
    if contract.SecType == "OPT" {
        return ibkr.MidPriceOrder(order.Direction, order.Quantity)
    }
}
```

#### Multi-Leg Order Sagas

An iron condor is four simultaneous option orders. Partial execution creates unintended exposure.

```
Workflow: Iron Condor on SPY
  Step 1: Sell OTM put      (filled) ✓
  Step 2: Buy further OTM put (filled) ✓
  Step 3: Sell OTM call      (filled) ✓
  Step 4: Buy further OTM call (FAILED) ✗
    → Compensation: unwind steps 1-3 immediately
    → All steps carry idempotency keys
    → Workflow state tracked explicitly
```

#### Order Lifecycle

```
Thesis Approved
  → Capability Token Minted (constraints, expiry, nonce)
    → Pre-trade Risk Check (deterministic, policy engine)
      → Smart Routing (size-based algo selection)
        → Order Submitted to IBKR
          → Fill Received → Position Opened
            → Position Monitored (kill conditions, thesis health, mark-to-market)
              → Exit Trigger (thesis complete, stop hit, time expired, health degraded)
                → Close Order Submitted
                  → Position Closed
                    → Episode Recorded → Belief Updated
```

### Pillar 6: The Book (Portfolio & P&L)

Know exactly where we stand at all times.

```
internal/book/
├── portfolio.go        # Portfolio state management
├── position.go         # Individual position tracking
├── pnl.go              # Real-time P&L calculation (realized + unrealized)
├── greeks.go           # Options greeks aggregation
├── mark.go             # Mark-to-market using IBKR prices
├── attribution.go      # P&L attribution by desk, domain, factor, time
├── factors.go          # Factor decomposition (anti-overfitting layer)
└── reconcile.go        # Reconcile internal state vs IBKR account state
```

#### Portfolio State

```
Per Desk:
  |-- Open positions (instrument, size, entry, current, unrealized P&L)
  |-- Closed positions (full history, realized P&L, holding period, exit reason)
  |-- Greeks exposure (delta, gamma, theta, vega — for options positions)
  |-- Capital allocated vs capital deployed vs cash available

Per Portfolio (all desks):
  |-- Gross and net exposure
  |-- Factor decomposition (direction, vol, sector, rates, etc.)
  |-- Cross-desk correlation matrix
  |-- Aggregate Greeks
  |-- Total P&L, Sharpe, max drawdown, win rate
  |-- Per-desk attribution
  |-- Per-domain attribution
  |-- Per-timeframe attribution (which hours produce alpha)
```

#### Broker Reconciliation

Every 60 seconds, compare internal book state against IBKR's actual positions. If there's a mismatch, IBKR is the source of truth.

```go
func (b *Book) Reconcile(ctx context.Context) error {
    // 1. Query IBKR for all positions via account API
    ibkrPositions := b.ibkr.GetPositions()

    // 2. Compare to internal state
    for _, pos := range ibkrPositions {
        internal := b.positions[pos.ContractID]
        if internal == nil || internal.Quantity != pos.Quantity {
            b.metrics.RecordDiscrepancy(pos)
            b.syncToIBKR(pos) // IBKR is source of truth
        }
    }

    // 3. Track discrepancy frequency (indicates bugs in our state management)
    // High discrepancy rate → alert, investigate state management bugs
}
```

This is critical for production — network failures, partial fills, race conditions between order submission and fill recording can all cause state drift.

#### Factor Decomposition

Portfolio-level factor decomposition detects hidden concentration:

* If 80% of P&L comes from being long volatility → not 10 strategies, one bet expressed 10 ways
* If 60% of exposure is correlated with oil prices → single commodity move can destroy the portfolio
* The CEO referee forces diversification when any single factor exceeds 25% of attribution

### Pillar 7: The Memory (Learning System)

The system gets smarter with every trade. MARS kernel concepts ported to Go.

```
internal/memory/
├── belief/
│   ├── graph.go        # BeliefGraph — trust/confidence per competence
│   ├── competence.go   # Competence key: (desk, capability, context, regime)
│   ├── autonomy.go     # Autonomy mode inference (MARS graduation)
│   ├── cascade.go      # Hierarchical + lateral belief propagation
│   └── regime.go       # Regime-conditional belief partitioning
├── engram/
│   ├── store.go        # Cached successful action plans (Layer 1 + Layer 2)
│   └── matcher.go      # Intent+State → past successful plan
├── episodic/
│   ├── store.go        # Every thesis ever run, full history
│   └── query.go        # "Show me all short trades on oil in Q4"
├── semantic/
│   ├── patterns.go     # Extracted patterns across thousands of trades
│   └── embeddings.go   # pgvector for semantic pattern matching
├── procedural/
│   ├── rules.go        # Evolved process rules (meta-learning)
│   └── evolution.go    # Rule mutation based on performance
├── backfill.go         # Synthetic belief generation from historical data
└── learn.go            # LearnWorker — outcome → belief updates
```

#### Belief Graph

```go
type CompetenceState struct {
    Key           string   // "(desk, capability, context, regime)"
    Trust         float64  // 0-1, starts at 0.55
    Confidence    float64  // 0-1, starts at 0.35
    SuccessCount  int
    FailureCount  int
    TotalPnL      float64
    SharpeRatio   float64
    LastUpdated   time.Time
}

// MARS graduation thresholds adapted for trading
func (c *CompetenceState) AutonomyMode() AutonomyMode {
    successRate := float64(c.SuccessCount) / float64(c.SuccessCount + c.FailureCount)
    if c.Trust >= 0.82 && c.Confidence >= 0.70 && successRate >= 0.75 {
        return Autonomous  // Can increase position sizes, less prosecution scrutiny
    }
    if c.Trust >= 0.65 && c.Confidence >= 0.50 {
        return Supervised  // Normal operation
    }
    return Restricted  // Reduced sizes, extra prosecution
}
```

#### Belief Backfill

Run the MARS cognitive loop over historical data as if the system had been alive. Output: manufactured institutional memory.

1. Assemble historical signal corpus (AiFW database + historical market data from IBKR)
2. For each historical signal, simulate: Would the scanner have flagged it? What thesis would research have formed? What was the actual market outcome 24/48/72 hours later?
3. Score each simulation against actual outcomes using the belief update math
4. Output: belief graph representing what the system would have believed if it had been running the entire time
5. Mount onto desks with confidence discount (30% haircut on backfilled belief strength)

```
Live belief:     Beta(successes + 1, failures + 1)
Backfilled:      Beta(successes * 0.7 + 1, failures * 1.3 + 1)
```

The system learns over time how much to trust backfilled vs live-earned beliefs per domain.

---

## The A/B Test

### Design

**Group A (20 desks): Full MARS belief architecture.**

* Backfilled beliefs from historical corpus
* Live belief accumulation from every trade outcome
* Engram formation and pattern recognition
* Earned autonomy (REASONING → AUTONOMOUS based on validated performance)
* Regime-conditional beliefs
* Group episodic memory
* Full cognitive loop: THINK → REMEMBER → PLAN → CHECK → ROUTE → ACT → LEARN

**Group B (20 desks): Control — same LLMs, no belief system.**

* Same models (same self-hosted Qwen, same Claude API for prosecution)
* Same Wire signals
* Same first principles seeding
* Same risk constraints
* Same desk structure (trios: trader, analyst, researcher)
* NO belief accumulation
* NO engrams — reasons from scratch every time
* NO earned autonomy — always in reasoning mode
* NO episodic memory — no learning from previous trades

**Both groups:**

* 20 desks each, $25k per desk
* Same domain distribution (paired: for each Group A desk, a matching Group B desk in the same domain with the same approach)
* Same CEO referee watching both groups
* Same 115-day evaluation period

### What the A/B Test Proves

If Group A outperforms Group B consistently over 30+ days:

* The MARS belief architecture generates alpha beyond raw LLM reasoning
* Accumulated institutional memory has measurable value
* The cognitive loop's learning mechanism produces better trades over time

If Group B matches or outperforms Group A:

* Raw LLM reasoning is sufficient; the belief layer adds complexity without value
* The backfill may have introduced noise rather than signal
* The system should be simplified

The A/B test isn't optional. It's the scientific validation of the entire MARS research program applied to a new domain.

---

## Anti-Overfitting Architecture

Five defensive layers, each addressing a different failure mode.

### Layer 1: Statistical Rigor

* Every belief carries a Beta posterior confidence interval, not just a point estimate
* Minimum 50 trials before a belief can reach trust > 0.75
* Minimum 100 trials before autonomous mode is possible
* Hypothesis testing: if the system tested 500 patterns to find 14 winners, that's expected from chance alone

### Layer 2: Regime-Conditional Beliefs + Transition Decay

* Beliefs partitioned by market regime
* Regime shift forces all affected capabilities back to reasoning mode
* Minimum 20 validated trades in new regime before autonomy can be restored
* The gap detector's `stale` category flags beliefs earned in non-current conditions

### Layer 3: Organizational Regularization

* Permanent desks stay general within their domain (volume and variety prevent single-pattern fixation)
* Temporary sub-teams overfit intentionally but don't carry permanent beliefs
* Independent groups working the same signal provide triangulation
* The desk discounts sub-team findings using broader experience
* Meta-skeptic (CEO) watches for correlated overconfidence

### Layer 4: Portfolio-Level Factor Decomposition

* Real-time factor attribution: how much P&L from each factor (direction, vol, sector, rates, etc.)
* If 80% of P&L from one factor → not 10 strategies, one bet expressed 10 ways
* CEO referee forces diversification when single-factor concentration exceeds threshold

### Layer 5: Periodic Belief Decay

* Every quarter, all beliefs decay 10-15% across the entire system
* Every desk must re-earn trust
* Prevents slow accumulation of stale confidence from extended favorable regimes
* Same principle as rotating traders across desks

---

## First Principles Seeding

Every desk and agent is seeded with foundational reasoning frameworks. These are NOT beliefs in the MARS sense. They are system prompts, reference documents, and reasoning substrates.

### Universal (All Desks)

* Market microstructure: order books, bid-ask spreads, market maker behavior, gamma hedging mechanics, options pinning, expiration dynamics
* Behavioral finance: loss aversion, anchoring, herding, overreaction to narrative, underreaction to base rates, disposition effect
* Risk management: Kelly criterion, correlation spikes in crises, drawdown > return, worst loss always ahead, leverage kills
* Historical case studies: Ackman COVID trade, Soros Bank of England, LTCM collapse, 2010 flash crash, GameStop squeeze, SVB collapse

### Options Desks

* Black-Scholes and beyond, Greeks as exposure language, variance risk premium, IV skew mechanics, theta non-linearity, volatility surface dynamics, term structure
* Put-call parity, early exercise, dividend impact, pin risk

### Macro Desks

* Fed transmission mechanism, yield curve inversion → recession lag, dollar wrecking ball, Soros reflexivity loops, carry trade mechanics, EM vulnerability to USD strength

### Corporate Desks

* Revenue recognition red flags, cash flow vs accrual divergence, insider transaction patterns, quality indicators (ROIC, FCF yield, capital allocation), institutional ownership signals

### Geopolitical Desks

* Cascade mapping frameworks, sanctions mechanics, commodity dependency maps, alliance structures, historical conflict-to-market-impact patterns

---

## LLM Architecture

### Self-Hosted (Unlimited, 24/7)

Running on Azure GPU VMs via vLLM:

**Qwen 3.5 72B — "The Workhorse"**

* Thesis formation, evidence synthesis, cascade mapping
* Deep research, filing analysis, multi-source synthesis
* Group conversations between desk trios
* 4x A100 80GB GPUs
* ~$10,500/month

**Qwen 3.5 7B — "The Scanner"**

* Signal scanning (is this tradeable?)
* News parsing and summarization
* Translation (all languages → English)
* Base rate lookup
* 2-3x T4 instances
* ~$1,600/month

### API (Pay-Per-Use, Surgical — 5% of calls)

**Claude Sonnet (via Anthropic API)**

* Prosecution / adversarial review
* Complex geopolitical synthesis requiring deep reasoning
* Council debates for large positions
* Final conviction scoring on high-value theses
* Meta-skeptic function (CEO)

### LLM Routing

```go
type LLMRouter struct {
    qwen7b  llm.Client  // Self-hosted, unlimited tokens
    qwen72b llm.Client  // Self-hosted, unlimited tokens
    claude  llm.Client  // API, metered — use sparingly
}

func (r *LLMRouter) Route(task LLMTask) llm.Client {
    switch task.Tier {
    case TierSpeed:    return r.qwen7b   // Translation, filtering, simple extraction
    case TierAnalysis: return r.qwen72b  // Research, synthesis, thesis formation
    case TierCritical: return r.claude   // Prosecution, council, critical decisions
    }
}
```

### Why Self-Hosted for Group Conversations

The desk trios have multi-turn conversations — researcher presents findings, analyst contextualizes, trader stress-tests. Each conversation is 50-100 LLM calls. With 40 desks having continuous conversations plus sub-teams, that's millions of calls per day.

At API pricing: ~$100k+/month. Self-hosted: ~$12k/month, unlimited. The group conversation model is only economically viable with self-hosted inference.

---

## Infrastructure: Azure Deployment

### Compute

| Resource | Purpose | Est. Monthly Cost |
|----------|---------|-------------------|
| 4x NC24ads_A100_v4 | Qwen 72B inference cluster | ~$10,500 |
| 2-3x NC8as_T4_v3 | Qwen 7B scanner/translation | ~$1,600 |
| 2x Standard_D8s_v5 | Go services, Redis, PostgreSQL | ~$700 |
| 1x Standard_E8s_v5 | Neo4j | ~$500 |
| Blob Storage | Signal archive, episode storage | ~$200 |
| Azure Cache for Redis | Hot state, signal queues | ~$300 |
| Azure Database for PostgreSQL | Book of record, audit trail | ~$400 |

**Total: ~$14,200/month**

With $135k in credits over ~115 days (~3.8 months): $135k / 3.8 = ~$35.5k/month budget. Infrastructure uses ~40% of budget, leaving headroom for scaling and Anthropic API costs.

### Data Flow

```
Sources → Wire (Go goroutines) → Redis Streams → Scanner (Qwen 7B)
  → Filtered signals → Desk channels (Go channels per desk)
    → Research (Qwen 72B group conversation)
      → Prosecution (Claude Sonnet API)
        → Risk Gate (deterministic Go)
          → Execution (IBKR TWS API)
            → Book (PostgreSQL)
              → Memory (Neo4j belief update)
                → Audit (PostgreSQL append-only log)
```

---

## Go Architecture

```
trading-floor/
  cmd/
    floor/                    # Main 24/7 daemon
      main.go
    backfill/                 # Historical belief generation
      main.go
    ctl/                      # CLI management (positions, P&L, kill switch, desk status)
      main.go

  internal/
    firm/                     # Organizational structure
      ceo.go                  # CEO referee: allocation, monitoring, forensics
      desk.go                 # Desk lifecycle, trio management, sub-team spawning
      seat.go                 # Seat abstraction (role + beliefs, model-agnostic)
      group.go                # Group dynamics, shared episodic memory
      subteam.go              # Temporary sub-team lifecycle

    wire/                     # Signal ingestion (shared utility)
      manager.go              # Fan-in coordinator, backpressure management
      feeds/
        news.go               # RSS, webhooks, scrapers
        market.go             # IBKR streaming quotes (TWS API)
        options.go            # Options chain data, IV surfaces
        economic.go           # Scheduled releases (BLS, Fed, Treasury)
        filings.go            # EDGAR 10-K/10-Q/8-K/Form 4
        social.go             # Twitter, Reddit, Telegram
        alternative.go        # Satellite, AIS, flight tracking
      translate.go            # Multi-language translation (Qwen 7B)
      normalize.go            # Signal normalization
      dedup.go                # Content hash + semantic dedup (threshold 0.92)
      embed.go                # Vector embedding for every signal

    scanner/                  # Signal → tradeable opportunity detection
      engine.go               # Main scanner loop (Qwen 7B, cheap, fast)
      filters/
        momentum.go           # Price/volume breakouts
        volatility.go         # IV vs RV divergence
        event.go              # Upcoming catalyst detection
        anomaly.go            # Unusual options activity, insider trades
        macro.go              # Cross-market correlation breaks
        sentiment.go          # Crowd positioning, consensus shifts
        cascade.go            # Event cascade detection (reflexivity engine)
      scorer.go               # Opportunity scoring (0-100)

    research/                 # Thesis formation (per desk)
      desk.go                 # Research orchestration (trio conversation)
      strategies/
        scalper.go
        event.go
        macro.go
        fundamental.go
        contrarian.go
        tail.go
      thesis.go               # Thesis data structure + health score
      prosecutor.go           # Adversarial challenge (Claude Sonnet)
      council.go              # Multi-archetype debate for large positions
      evidence.go             # Evidence gathering and weighting
      antiportfolio.go        # Rejected thesis tracking + counterfactual P&L

    risk/                     # The Risk Desk (deterministic, no LLM)
      gate.go                 # Pre-trade validation
      limits/
        position.go
        portfolio.go
        drawdown.go
        correlation.go
        greeks.go
        concentration.go
      killswitch.go           # Emergency halt
      tokens.go               # Capability token minting and validation
      circuit_breaker.go      # Per-strategy circuit breakers
      regime.go               # Regime detection and transition handling

    execution/                # Order management
      manager.go              # Order lifecycle state machine
      ibkr/
        client.go             # Wraps scmhub/ibapi (TWS API socket)
        orders.go             # Order type builders
        market_data.go        # Real-time streaming
        contracts.go          # Contract lookup and resolution
        account.go            # Position/balance queries
        connection.go         # Connection management, auto-reconnect
      router.go               # Smart order routing (size-based)
      slicer.go               # Large order slicing (TWAP, iceberg)
      saga.go                 # Multi-leg saga orchestration
      fill_tracker.go         # Fill tracking, partial fills

    book/                     # Portfolio tracking
      portfolio.go            # Positions, exposure
      position.go             # Individual position tracking
      pnl.go                  # P&L calculation (realized + unrealized)
      greeks.go               # Portfolio Greeks (delta, gamma, theta, vega)
      mark.go                 # Real-time mark-to-market
      attribution.go          # Performance attribution (desk, domain, factor, time)
      factors.go              # Factor decomposition
      reconcile.go            # Reconcile internal state vs IBKR

    memory/                   # The Belief System (MARS core)
      belief/
        graph.go              # Belief graph (Layer 1 + Layer 2)
        competence.go         # Competence key generation
        autonomy.go           # Autonomy mode inference
        cascade.go            # Hierarchical + lateral belief cascade
        regime.go             # Regime-conditional belief partitioning
      engram/
        store.go              # Engram storage, cross-desk aggregation
        matcher.go            # Intent+State → past successful plan
      episodic/
        store.go              # Trade episode storage (full lifecycle)
        query.go              # Episode querying
      semantic/
        patterns.go           # Pattern extraction across episodes
        embeddings.go         # pgvector for semantic pattern matching
      procedural/
        rules.go              # Evolved rules from observed patterns
        evolution.go          # Rule mutation based on performance
      backfill.go             # Synthetic belief generation from historical data
      learn.go                # LearnWorker — outcome → belief updates

    graph/                    # Knowledge Graph (Neo4j)
      entities.go             # Companies, people, sectors, instruments
      relationships.go        # Connections between entities
      cascade.go              # Event cascade path modeling
      query.go                # Graph traversal for trade discovery
      ontology.go             # Pack ontology → graph structure

    observe/                  # Observability
      audit.go                # Immutable audit log (every decision, every trade)
      metrics.go              # Prometheus metrics
      trace.go                # Distributed tracing
      dashboard.go            # Real-time status (desks, P&L, beliefs, regime)

    llm/                      # LLM abstraction
      client.go               # Unified client interface
      selfhosted.go           # vLLM self-hosted client
      anthropic.go            # Claude API client
      router.go               # Route calls to appropriate model tier

    ab/                       # A/B test infrastructure
      experiment.go           # Experiment definition and tracking
      control.go              # Control group (no beliefs) desk wrapper
      treatment.go            # Treatment group (full MARS) desk wrapper
      analysis.go             # Statistical comparison of groups

  pkg/
    signal/                   # Signal types and interfaces
    ibkr/                     # IBKR API types (shared with execution)
    model/                    # Shared domain models

  deploy/
    packs/
      trading-v1/
        ontology.json
        capabilities.json
        policies.json
        objectives.json
    azure/
      infrastructure.bicep    # Azure infrastructure as code
      gpu-vms.bicep           # GPU VM provisioning
    docker/
      Dockerfile              # Single binary, scratch base

  store/
    migrations/               # PostgreSQL migrations
    seeds/
      first_principles/       # Seeding documents per domain
        universal.md
        options.md
        macro.md
        corporate.md
        geopolitical.md
        behavioral.md
        risk_management.md
        case_studies.md
```

### The Core Loop

```go
func (f *Floor) Run(ctx context.Context) error {
    signals := f.wire.Subscribe(ctx) // fan-in all sources

    for signal := range signals {
        // Every signal goes to every desk
        for _, desk := range f.desks {
            go desk.Process(ctx, signal)
        }
    }
    return nil
}

func (d *Desk) Process(ctx context.Context, sig signal.Signal) {
    // Scanner: is this relevant to my domain? Is it tradeable?
    opportunity, ok := d.scanner.Evaluate(ctx, sig, d.domain)
    if !ok { return }

    // Research: group conversation — researcher, analyst, trader
    thesis, err := d.research.Investigate(ctx, opportunity, d.group)
    if err != nil || thesis.Conviction < d.minConviction {
        d.memory.RecordRejection(ctx, opportunity, thesis) // anti-portfolio
        return
    }

    // Prosecution: adversarial challenge (Claude Sonnet)
    prosecution := d.prosecutor.Challenge(ctx, thesis, d.beliefs)
    if prosecution.Verdict == Killed {
        d.antiPortfolio.Record(ctx, thesis, "prosecution_killed")
        return
    }
    thesis.ApplyProsecution(prosecution)

    // Council: multi-archetype debate if large position
    if thesis.PositionSize > d.councilThreshold {
        verdict := d.council.Debate(ctx, thesis)
        thesis.ApplyVerdict(verdict)
        if verdict.Rejected {
            d.antiPortfolio.Record(ctx, thesis, "council_rejected")
            return
        }
    }

    // Risk: deterministic check against pack policies
    order := thesis.ToOrder(d.trader)
    token, decision := d.risk.Check(ctx, order, d.book, f.portfolio)
    if !decision.Allowed {
        d.antiPortfolio.Record(ctx, thesis, "risk_blocked")
        return
    }

    // Execute with capability token
    fill, err := d.execution.Submit(ctx, token, decision.AdjustedOrder)
    if err != nil { return }

    // Book it
    d.book.OpenPosition(ctx, fill, thesis)

    // Memory: record episode start
    d.memory.RecordEntry(ctx, thesis, fill, d.ID)
}
```

---

## Data Schema

### PostgreSQL

```sql
-- Signals (with vector embeddings for cascade detection)
CREATE TABLE signals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source          TEXT NOT NULL,
    type            TEXT NOT NULL,
    category        TEXT,
    content         TEXT,
    language        TEXT DEFAULT 'en',
    translated      TEXT,
    entities        JSONB,
    urgency         FLOAT DEFAULT 0,
    strength        FLOAT DEFAULT 0,
    direction       TEXT,
    embedding       vector(1536),
    content_hash    TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_signals_embedding ON signals USING ivfflat (embedding vector_cosine_ops);
CREATE INDEX idx_signals_content_hash ON signals (content_hash);
CREATE INDEX idx_signals_created_at ON signals (created_at);

-- Opportunities (scanner output)
CREATE TABLE opportunities (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    signal_ids      UUID[] NOT NULL,
    instruments     JSONB NOT NULL,
    direction       TEXT,
    urgency         FLOAT,
    score           FLOAT,
    category        TEXT,
    cascade_info    JSONB,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- Theses (research output)
CREATE TABLE theses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    opportunity_id  UUID REFERENCES opportunities(id),
    desk_id         TEXT NOT NULL,
    strategy        TEXT NOT NULL,
    instrument      JSONB NOT NULL,
    direction       TEXT NOT NULL,
    conviction      FLOAT,
    health          FLOAT DEFAULT 0.5,
    evidence        JSONB,
    counter_args    JSONB,
    entry_price     FLOAT,
    target_price    FLOAT,
    stop_loss       FLOAT,
    position_size   FLOAT,
    time_horizon    INTERVAL,
    kill_rules      JSONB,
    status          TEXT DEFAULT 'embryo',
    prosecution     JSONB,
    council_verdict JSONB,
    outcome         JSONB,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);

CREATE INDEX idx_theses_desk ON theses (desk_id);
CREATE INDEX idx_theses_status ON theses (status);

-- Positions (live book)
CREATE TABLE positions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thesis_id       UUID REFERENCES theses(id),
    desk_id         TEXT NOT NULL,
    instrument      JSONB NOT NULL,
    direction       TEXT NOT NULL,
    quantity        FLOAT NOT NULL,
    entry_price     FLOAT NOT NULL,
    current_price   FLOAT,
    unrealized_pnl  FLOAT DEFAULT 0,
    realized_pnl    FLOAT DEFAULT 0,
    ibkr_order_id   INT,
    ibkr_contract_id INT,
    status          TEXT DEFAULT 'open',
    opened_at       TIMESTAMPTZ DEFAULT NOW(),
    closed_at       TIMESTAMPTZ
);

CREATE INDEX idx_positions_desk ON positions (desk_id);
CREATE INDEX idx_positions_status ON positions (status);

-- Belief Graph (competence states)
CREATE TABLE competence_states (
    key             TEXT PRIMARY KEY,
    desk_id         TEXT NOT NULL,
    capability      TEXT NOT NULL,
    context         TEXT,
    regime          TEXT,
    trust           FLOAT DEFAULT 0.55,
    confidence      FLOAT DEFAULT 0.35,
    success_count   INT DEFAULT 0,
    failure_count   INT DEFAULT 0,
    total_pnl       FLOAT DEFAULT 0,
    sharpe          FLOAT DEFAULT 0,
    autonomy_mode   TEXT DEFAULT 'restricted',
    is_backfilled   BOOLEAN DEFAULT false,
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_competence_desk ON competence_states (desk_id);

-- Engrams (cached winning plays)
CREATE TABLE engrams (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    intent_key      TEXT NOT NULL,
    context_pattern TEXT NOT NULL,
    capability      TEXT NOT NULL,
    desk_id         TEXT,
    layer           INT DEFAULT 1,
    action_plan     JSONB NOT NULL,
    success_count   INT DEFAULT 1,
    failure_count   INT DEFAULT 0,
    avg_return      FLOAT DEFAULT 0,
    sharpe          FLOAT DEFAULT 0,
    regime_tags     TEXT[],
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_engrams_intent ON engrams (intent_key);
CREATE INDEX idx_engrams_desk ON engrams (desk_id);

-- Anti-Portfolio (rejected theses — counterfactual tracking)
CREATE TABLE anti_portfolio (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thesis_snapshot  JSONB NOT NULL,
    rejection_reason TEXT NOT NULL,
    desk_id          TEXT NOT NULL,
    strategy         TEXT NOT NULL,
    instrument       JSONB NOT NULL,
    direction        TEXT NOT NULL,
    would_have_entry FLOAT,
    would_have_exit  FLOAT,
    would_have_pnl   FLOAT,
    would_have_outcome TEXT,
    evaluated_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_anti_portfolio_desk ON anti_portfolio (desk_id);
CREATE INDEX idx_anti_portfolio_reason ON anti_portfolio (rejection_reason);

-- Episodes (full trade lifecycle for episodic memory)
CREATE TABLE episodes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thesis_id       UUID REFERENCES theses(id),
    desk_id         TEXT NOT NULL,
    strategy        TEXT NOT NULL,
    instrument      JSONB NOT NULL,
    regime          TEXT,
    entry_signal    JSONB,
    entry_fill      JSONB,
    exit_signal     JSONB,
    exit_fill       JSONB,
    holding_period  INTERVAL,
    realized_pnl    FLOAT,
    return_pct      FLOAT,
    risk_reward     FLOAT,
    error_class     TEXT,
    lesson          TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_episodes_desk ON episodes (desk_id);
CREATE INDEX idx_episodes_strategy ON episodes (strategy);

-- Audit Log (immutable, append-only)
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    timestamp       TIMESTAMPTZ DEFAULT NOW(),
    desk_id         TEXT,
    event_type      TEXT NOT NULL,
    event_data      JSONB NOT NULL,
    thesis_id       UUID,
    position_id     UUID,
    order_id        INT
);

CREATE INDEX idx_audit_timestamp ON audit_log (timestamp);
CREATE INDEX idx_audit_desk ON audit_log (desk_id);
CREATE INDEX idx_audit_type ON audit_log (event_type);

-- Desk Performance (daily snapshots for CEO referee)
CREATE TABLE desk_performance (
    id              BIGSERIAL PRIMARY KEY,
    desk_id         TEXT NOT NULL,
    date            DATE NOT NULL,
    ab_group        TEXT NOT NULL,
    domain          TEXT NOT NULL,
    daily_pnl       FLOAT DEFAULT 0,
    daily_return    FLOAT DEFAULT 0,
    trades_taken    INT DEFAULT 0,
    trades_won      INT DEFAULT 0,
    sharpe_30d      FLOAT,
    max_drawdown    FLOAT,
    capital_allocated FLOAT,
    capital_deployed FLOAT,
    autonomy_level  TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(desk_id, date)
);
```

### Neo4j Graph Model

```
(Company)-[:OPERATES_IN]->(Sector)
(Company)-[:TRADES_AS]->(Ticker)
(Person)-[:HOLDS_TITLE_AT]->(Company)
(Company)-[:SUPPLIES]->(Company)
(Company)-[:COMPETES_WITH]->(Company)
(Event)-[:AFFECTS]->(Company)
(Event)-[:CAUSES]->(Event)
(Event)-[:LOCATED_IN]->(Country)
(Signal)-[:MENTIONS]->(Entity)
(Thesis)-[:TRADES]->(Instrument)
(Instrument)-[:CORRELATES_WITH]->(Instrument)
(Sector)-[:EXPOSED_TO]->(MacroFactor)
(Country)-[:PRODUCES]->(Commodity)
(Country)-[:CONTROLS]->(Chokepoint)
```

---

## 115-Day Timeline

### Days 1-14: Foundation

* Azure GPU VMs provisioned, vLLM serving Qwen models 24/7
* PostgreSQL + pgvector, Redis, Neo4j running on Azure
* Go project scaffolded with full directory structure
* IBKR paper account connected — can read markets, submit paper orders, get fills
* Basic Wire running (news RSS + IBKR market data streaming)
* Signal schema + dedup + embedding pipeline implemented
* Belief graph schema in PostgreSQL
* Pack structure defined (ontology, capabilities, policies, objectives)

**Deliverable:** Infrastructure running. Can query markets and LLMs are serving.

### Days 15-28: First Desks

* 5 desks stood up (one per major domain) with full trio
* Scanner agents running against market data
* Simple thesis formation (Qwen 72B)
* Prosecution layer working (Claude Sonnet)
* Risk gate implemented (position limits, daily loss, kill switch, capability tokens)
* First paper trades executing via IBKR
* Book tracking P&L in real time + reconciliation against IBKR every 60s
* Audit trail logging every decision
* Belief graph recording outcomes
* Anti-portfolio recording rejected theses

**Deliverable:** 5 desks making paper trades. P&L visible. Beliefs accumulating.

### Days 29-42: Options + Backfill

* Options chain analysis working
* Multi-leg order construction (saga workflows)
* Smart order routing (size-based algo selection)
* IV surface analysis, vol regime detection
* Full backfill runner built — processes historical corpus against market data
* Backfilled beliefs generated and mounted on desks (30% haircut)
* Sub-team spawning mechanism working
* Neo4j knowledge graph building entity relationships
* Scale to 20 desks

**Deliverable:** Options trading active. Backfilled beliefs seeded. 20 desks running.

### Days 43-56: Full Firm + A/B

* Scale to 40 desks (20 Group A + 20 Group B)
* A/B test infrastructure tracking both groups
* CEO referee operational (monitoring, correlation detection, allocation)
* Council convening for large positions
* Full multi-language Wire (translation pipeline, social signal, Telegram)
* Deep cascade mapping across domains
* Group conversation dynamics between desk trios
* Regime detection working across all dimensions
* Anti-portfolio counterfactual evaluation running retroactively

**Deliverable:** Full 40-desk firm running. A/B test active. 24/7 operation.

### Days 57-70: Learning Kicks In

* Enough trade history for pattern extraction
* Engrams forming for proven patterns
* Belief graph differentiating winning from losing approaches
* Regime-conditional beliefs accumulating
* Autonomous mode activating for high-trust capabilities on Group A desks
* CEO performing first capital reallocations based on performance data
* Thesis health degradation driving position management

**Deliverable:** Group A desks beginning to show differentiation from Group B (or not).

### Days 71-84: Hardening + Scale

* Prosecution gets harder (informed by accumulated failure patterns + anti-portfolio feedback)
* Risk gates tightened based on observed drawdowns
* Factor decomposition detecting hidden concentrations
* Anti-overfitting layers all active (including quarterly belief decay)
* Sub-teams deploying for deep investigations
* Performance attribution revealing which domains/approaches/timeframes work

**Deliverable:** System is self-tuning. Weak desks identified and capital reallocated.

### Days 85-100: Validation

* 30+ days of continuous operation with full A/B test data
* Statistical significance tests on Group A vs Group B performance
* Per-desk, per-domain, per-approach performance reports
* Belief graph quality analysis (are beliefs predictive?)
* Engram hit rate analysis (do proven patterns actually work?)
* Full factor decomposition of portfolio P&L

**Deliverable:** The answer to "does the belief architecture generate alpha?"

### Days 100-115: Decision Point

If profitable and A/B proves belief architecture value:
* Document which domains, approaches, and belief configurations work
* Optimize capital allocation toward proven desks
* Begin planning transition from paper to live trading with small real capital
* The system, the belief graph, and the track record become the product

If not profitable:
* The belief graph tells you exactly WHY — which archetypes, instruments, conditions, reasoning failures
* Iterate on specific failure modes
* The 115 days of data are still the most comprehensive dataset ever collected on LLM-driven trading

---

## Success Criteria

### 30-Day Forward Test

| Metric | Target |
|--------|--------|
| Aggregate daily return | ~2% (portfolio level) |
| Portfolio Sharpe ratio | > 2.0 |
| Maximum drawdown | < 10% |
| Win rate (per trade) | > 52% |
| Group A vs Group B | Statistically significant outperformance (p < 0.05) |
| System uptime | > 99% |
| Belief predictiveness | Engram hit rate > base rate |
| Autonomous mode trades | Outperform reasoning mode trades |
| Anti-portfolio | Consistently unprofitable (prosecutor is doing its job) |
| IBKR reconciliation | 100% match rate |

### What Constitutes Proof

A month of consistent positive aggregate returns across 40 desks, with full audit trail showing reasoning, beliefs, thesis formation, prosecution, and execution — against live market data with simulated execution. Plus statistical proof that the belief architecture outperforms the control group.

That's not a backtest. That's a forward test. That's fundable.

---

## MARS Component Mapping

| MARS Concept | Trading Floor Implementation |
|-------------|------------------------------|
| Intent | Trade signal |
| Capability | Trade structure (equity.long, options.straddle, etc.) |
| Agent | Desk seat (trader, analyst, researcher) |
| Pack | Trading pack (ontology, capabilities, policies, objectives) |
| Belief graph | Per-desk trust in capabilities x contexts x regimes |
| Engram | Proven trading pattern with success/failure counts |
| Gap detector | Market regime detection + position health monitoring |
| Policy engine | Risk desk (deterministic constraints) |
| Cognitive loop | THINK → REMEMBER → PLAN → CHECK → ROUTE → ACT → LEARN |
| Capability token | Pre-trade authorization with constraints |
| Tool Gateway | Risk gate + order validator |
| Manager agent | CEO referee |
| Agent pool | Desk roster (40 desks x 3 seats) |
| Layer 1 (cloud) | Cross-desk proven patterns |
| Layer 2 (org) | Per-desk earned beliefs |
| Adjudication | Trade resolution + thesis validation |
| Workflow orchestrator | Multi-leg order saga |
| Thompson Sampling | Capital allocation across desks |

---

*Built in Go. Single binary. Runs forever.*
