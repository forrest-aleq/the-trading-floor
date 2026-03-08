# The Trading Floor: Autonomous Trading System Design Document

## Version 0.1 — Architecture Specification

---

## Executive Summary

This document specifies the architecture for an autonomous trading system built on the MARS cognitive architecture. The system operates as a virtual trading firm — not a flat swarm of agents, but an organizational hierarchy with desks, roles, authority, capital allocation, and accountability.

The core thesis: alpha exists in the synthesis of information that no single human or institution can hold simultaneously. Not because the information is secret, but because there's too much of it, in too many languages, across too many sources, for any human to correlate. The cost of intelligence has collapsed. This system applies that intelligence correctly, continuously, and faster than any prior market participant.

The system runs 24/7 against real market data via thinkorswim paper trading. All agents run the full MARS belief architecture — the 12-test matrix and 500-cycle TAMI run already proved its superiority on tasks like this. This is the next phase of that research. The experiment tests organizational design, not whether MARS works.

Three experimental groups, all MARS-powered:

* **Group A — The Firm (20 desks × $25,000 = $500,000):** Full organizational units. Permanent trios (trader, analyst, researcher) covering broad domains. Tests whether deep organizational intelligence with group dynamics, cascade mapping, and multi-source synthesis produces alpha. Target: 2% daily aggregate.
* **Group B — The Specialists (20 desks × $25,000 = $500,000):** Full MARS belief architecture but individual agents per desk, not trios. Same domains, same signals, same first principles. Tests whether the GROUP structure (the trio, the conversation, the disagreement) is what generates alpha, or whether a single capable agent with deep beliefs performs equally well. Target: 2% daily aggregate.
* **Group C — The Hunters (200 micro-agents × $500 = $100,000):** Extreme niche specialization. Each agent gets a narrow domain and must find one repeatable edge — its signature move. Constraint forces discovery. Tests whether alpha lives in breadth of synthesis or depth of repetition. Target: 3% daily per agent.

Total paper trading account: $1,100,000. Some agents backfilled with market knowledge and first principles. Others backfilled with actual simulated trading experience — episodes of thesis formation, execution, and resolution replayed through the belief graph. The backfill comparison reveals whether inherited knowledge or inherited experience produces better outcomes.

Built in Go. Single binary. Runs forever.

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
* **Cross-desk correlation monitoring** : detects when the firm is unintentionally concentrated in one direction across multiple desks. Forces diversification or hedging when portfolio-level exposure exceeds thresholds
* **Desk creation and destruction** : spawns new desks in productive domains, shuts down desks in dead domains, reallocates capital
* **Performance forensics** : when a desk loses money, examines belief graph quality, thesis reasoning, prosecution effectiveness, group dynamics. Findings inform future desk construction
* **Meta-skeptic function** : watches for correlated confidence across desks. When all desks lean the same direction, asks "are these views actually independent or downstream of the same data source?" Has authority to force all desks into reasoning mode during regime transitions

**What it does NOT do:**

* Approve or reject individual trades
* Override desk-level thesis formation
* Route signals to specific desks
* Manage individual positions

**The CEO's belief graph** operates one level of abstraction above trading beliefs. It holds meta-beliefs: "Energy desk in high-vol regimes produces Sharpe 2.1." "Macro desk in trending regimes produces Sharpe 1.4 but with lower drawdown." "Tail desk loses money 11 months out of 12 but the 12th month pays for everything." These meta-beliefs drive capital allocation.

### The Desks

Each desk is a permanent group of three agents: Trader, Analyst, Researcher. They cover a domain. They persist indefinitely. Their group belief graph compounds over months.

**The Researcher** expands the search space. Ingests signals, follows threads, gathers evidence, finds non-obvious connections. Lives closest to the raw information.

**The Analyst** compresses the search space into structure. Contextualizes findings against history, models cascades, calculates base rates, forms theses with conviction scores. The analyst is also responsible for challenging whether current beliefs were earned in conditions that still hold (regime awareness).

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

**40 desks = ~8 domains × ~5 competing approaches per domain**

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

The MARS cognitive architecture — proven in the 12-test matrix (3 agents × 4 belief conditions × 17 tasks) and the 500-cycle TAMI run — provides the brain. This section specifies how each MARS component maps to the trading floor.

### The Cognitive Loop

MARS: THINK → REMEMBER → PLAN → CHECK → ROUTE → ACT → LEARN

Trading Floor:

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

6. ACT      — Submit order to thinkorswim via API.
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

* Trade returns 3× risk taken → strong positive signal
* Trade loses 0.5× risk → mild negative signal
* Trade loses 2× risk (gap through stop) → severe negative signal
* Boundary violation (blew through risk limit) → 10× moral asymmetry multiplier

#### Regime-Conditional Beliefs (Paper #4 — Critical)

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

* **unsafe** : thesis invalidated by new information → close immediately
* **stale** : position held longer than thesis timeframe → reassess
* **blocking** : can't exit (no liquidity, market halted) → flag for desk attention

#### Correlation Between Beliefs

Beliefs propagate not just hierarchically (within pack tree) but laterally (across correlated instruments and desks).

If a desk loses on 5 consecutive oil-related trades:

* Negative signal for specific oil capabilities
* Attenuated negative signal for the entire energy sector thesis
* Attenuated negative signal for the macro archetype that generated those trades
* Informational signal for Wire sources that produced those signals

Neo4j models these relationships as graph edges. Belief updates propagate through them with configurable attenuation.

#### Adversarial Market Error Classification

Beyond MARS's three categories:

| Category                 | Description                                       | Belief Update          |
| ------------------------ | ------------------------------------------------- | ---------------------- |
| `thesis_failure`       | Thesis was wrong, market moved against            | Full negative update   |
| `execution_friction`   | Thesis correct but slippage/fills degraded return | Attenuated negative    |
| `infrastructure_error` | Broker API, network, timeout                      | Skip — no update      |
| `policy_block`         | Risk system blocked the trade                     | Record as policy event |
| `market_halt`          | Circuit breaker, trading halt                     | Skip — no update      |

### Gap Detector: Trading Adaptation

| Category           | Trading Translation                                                                                                                                | Priority    |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- | ----------- |
| **unsafe**   | Flash crash detected, extreme correlation breakdown, liquidity evaporation, kill switch conditions, thesis invalidated for open position           | 1 (highest) |
| **blocking** | Broker API down, market halted, position limit reached, can't exit position                                                                        | 2           |
| **unknown**  | No base rate for this contract type, IV data stale, earnings date unconfirmed, regime classification uncertain                                     | 3-5         |
| **stale**    | Market regime shifted since beliefs earned, strategy unvalidated in current conditions, model version changed, position held past thesis timeframe | 7-8         |

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

| Tier | Source            | When Used                                                                               | Cost      |
| ---- | ----------------- | --------------------------------------------------------------------------------------- | --------- |
| 1    | Execution status  | Order filled, position opened/closed                                                    | Free      |
| 2    | P&L calculation   | Trade resolved, realized return computed                                                | Free      |
| 3    | Thesis validation | Did the thesis play out? (LLM evaluates original thesis against what actually happened) | Medium    |
| 4    | Desk review       | Desk trio evaluates trade quality holistically                                          | Expensive |

Low-conviction autonomous trades: Tier 1+2 only.
High-conviction thesis-driven trades: Tier 1+2+3.
Novel or large positions: Tier 1+2+3+4.

---

## The Wire: Signal Ingestion

Shared utility. Every desk sees every signal. Each desk filters through its own lens.

### Signal Sources

```
The Wire
  |
  |-- News (RSS, webhooks, scrapers)
  |     |-- Major wire services (Reuters, AP, Bloomberg headlines)
  |     |-- Financial news (WSJ, FT, CNBC, MarketWatch)
  |     |-- Sector-specific sources
  |     |-- International sources (in original languages)
  |
  |-- Market Data (thinkorswim streaming)
  |     |-- Real-time quotes (equities, options, futures, forex)
  |     |-- Options chains and IV surfaces
  |     |-- Volume and order flow
  |     |-- Level 2 / market depth
  |
  |-- Economic Data (scheduled releases)
  |     |-- BLS (employment, CPI, PPI)
  |     |-- Fed (FOMC, minutes, speeches, Beige Book)
  |     |-- Treasury (auctions, TIC flows)
  |     |-- Global central banks (BOJ, ECB, BOE, PBOC)
  |     |-- PMI, GDP, retail sales, housing
  |
  |-- Filings (EDGAR)
  |     |-- 10-K, 10-Q annual/quarterly reports
  |     |-- 8-K material events
  |     |-- Form 4 insider transactions
  |     |-- 13-F institutional holdings
  |     |-- Proxy statements
  |
  |-- Social Signal
  |     |-- Twitter/X (journalists, officials, analysts, OSINT)
  |     |-- Reddit (WallStreetBets, sector-specific)
  |     |-- Telegram channels (geopolitical, military, regional)
  |     |-- Discord communities
  |
  |-- Alternative Data
  |     |-- Satellite imagery services (shipping, construction, agriculture)
  |     |-- Flight tracking (military and commercial)
  |     |-- AIS vessel tracking (shipping lanes, port activity)
  |     |-- Weather/climate data (NOAA, commodity impact)
  |
  |-- Cross-Market
  |     |-- Futures (S&P, Nasdaq, Russell, Dow)
  |     |-- Forex majors and EM currencies
  |     |-- Commodities (oil, gold, copper, agricultural)
  |     |-- Fixed income (yields, spreads, credit)
  |     |-- Crypto (as sentiment/risk indicator)
```

### Signal Schema

Every piece of information enters the system as a normalized Signal:

```go
type Signal struct {
    ID        string            `json:"id"`
    Source    string            `json:"source"`
    Type      SignalType        `json:"type"`      // news, price, economic, filing, social, alternative
    Category  string            `json:"category"`  // geopolitical, macro, corporate, flow, etc.
    Timestamp time.Time         `json:"timestamp"`
    Urgency   float64           `json:"urgency"`   // 0.0-1.0
    Strength  float64           `json:"strength"`  // 0.0-1.0
    Direction *Direction        `json:"direction"`  // bullish, bearish, neutral, or nil
    Entities  []Entity          `json:"entities"`  // companies, people, instruments, countries
    Languages []string          `json:"languages"` // original languages
    Raw       json.RawMessage   `json:"raw"`       // original payload
    Translated string           `json:"translated"` // English translation if non-English
}
```

### Multi-Language Processing

Self-hosted Qwen 7B handles translation. Critical for:

* Farsi-language shipping/military channels
* Arabic-language geopolitical intelligence
* Chinese economic data and policy signals
* Japanese central bank communications
* Turkish, Russian regional intelligence

Translation is a Wire function, not a desk function. Desks receive translated signals. The original language is preserved for provenance.

---

## The Execution Layer

### thinkorswim Integration

Schwab/thinkorswim API provides:

* Real-time and historical market data
* Full options chain data with Greeks
* Paper trading with realistic fills
* Equity, options, futures, and forex order submission
* Account positions and P&L
* Streaming quotes via websocket

### Order Lifecycle

```
Thesis Approved
  → Order Created (pending)
    → Pre-trade risk check (deterministic, policy engine)
      → Capability token minted (constraints, expiry, nonce)
        → Order Submitted to thinkorswim
          → Fill Received → Position Opened
            → Position Monitored (kill conditions, mark-to-market)
              → Exit Trigger (thesis complete, stop hit, time expired, invalidated)
                → Close Order Submitted
                  → Position Closed
                    → Episode Recorded → Belief Updated
```

### Capability Tokens for Trading

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

### Multi-Leg Orders as Saga Workflows

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

---

## The Book: Portfolio Tracking

### Position Management

The Book is the source of truth. What do we own? What are we making/losing?

```
Per Desk:
  |-- Open positions (instrument, size, entry, current, unrealized P&L)
  |-- Closed positions (full history, realized P&L, holding period, exit reason)
  |-- Greeks exposure (delta, gamma, theta, vega — for options positions)
  |-- Capital allocated vs capital deployed vs cash available

Per Portfolio (all desks):
  |-- Gross and net exposure
  |-- Factor decomposition (how much P&L from each factor: direction, vol, sector, etc.)
  |-- Cross-desk correlation matrix
  |-- Aggregate Greeks
  |-- Total P&L, Sharpe, max drawdown, win rate
  |-- Per-desk attribution
  |-- Per-domain attribution
  |-- Per-timeframe attribution (which hours of day produce alpha)
```

### Real-Time Mark-to-Market

Every open position marked to current market price continuously. Unrealized P&L feeds the CEO referee's monitoring. If a desk's unrealized losses hit the daily loss limit, the desk is halted for the day automatically.

### Factor Decomposition (Anti-Overfitting Layer)

Portfolio-level factor decomposition detects hidden concentration:

* If 80% of P&L comes from being long volatility, the system has one bet expressed ten ways
* If 60% of exposure is correlated with oil prices, a single commodity move can destroy the portfolio
* The CEO referee forces diversification when any single factor exceeds 25% of attribution

---

## The Risk System

### Hard Rules (Deterministic, No LLM, No Override)

```
Per Trade:
  - Position size ≤ X% of desk capital
  - Valid capability token required
  - Conviction score ≥ minimum threshold
  - Kill conditions defined
  - Prosecution survived

Per Desk:
  - Daily loss ≤ 3% of desk capital → auto-halt for day
  - Max open positions ≤ 10
  - Max correlated positions ≤ 3
  - Max single position ≤ 20% of desk capital

Per Portfolio:
  - Total drawdown ≤ 10% of portfolio → CEO forces regime assessment
  - Total drawdown ≤ 15% of portfolio → KILL SWITCH — halt everything
  - Single factor exposure ≤ 25% of portfolio
  - Regime shift detected → all desks drop to reasoning mode
```

### Kill Switch

Always one signal away. Three trigger conditions:

1. **Portfolio drawdown** exceeds 15% → halt all trading, close all positions, CEO reconvenes desks
2. **System anomaly** detected (correlated failures across desks, data feed corruption, broker API instability) → halt and diagnose
3. **Manual trigger** — human operator can halt at any time

---

## Belief Backfill: Synthetic Experience Generation

### The Mechanism

Run the MARS cognitive loop over historical data as if the system had been alive. The output is a belief graph — manufactured institutional memory.

There are two fundamentally different types of backfill, and the experiment tests which one produces better outcomes.

### Type 1: Knowledge Backfill

Seeds agents with domain understanding. The agent knows ABOUT trading without having traded.

**Process:**

1. First principles documents loaded as system context (market microstructure, behavioral finance, options theory, macro frameworks, risk management, historical case studies)
2. Domain-specific expertise loaded per niche (for an oil futures agent: refinery economics, OPEC dynamics, strategic petroleum reserve mechanics, seasonal demand patterns, contango/backwardation theory)
3. Historical signal corpus from AiFW database processed into awareness — the agent knows what kinds of signals exist and what they typically mean
4. Base rate libraries compiled — "earnings surprises of >10% lead to post-earnings drift of X% on average with Y% consistency"

**What it produces:** An agent that can reason well about markets from day one. It has the right mental models. It understands the instruments. It knows the theory. But it has zero beliefs in the MARS sense — no trust earned, no engrams, no confidence intervals. It's a smart beginner.

**Analogy:** A freshly minted MBA who read every trading book but hasn't placed a trade.

### Type 2: Experience Backfill

Seeds agents with simulated trading experience. The agent has "lived through" hundreds of trades without having placed them in real time.

**Process:**

1. **Assemble historical signal corpus** from AiFW database (every article processed, every entity extracted, every SPS score assigned) plus historical market data from thinkorswim
2. **For each historical signal** , simulate the full trading loop:

* Would the scanner have flagged it as tradeable?
* What thesis would research have formed? (Run Qwen 72B on the historical signal with the market context as of that date)
* What trade structure would the trader have selected? (Given the options chain, IV surface, and liquidity at that historical point)
* What actually happened? (Look up the market data 24/48/72 hours later)
* Was the thesis correct? Did the trade make or lose money? How much?

1. **Run each simulated trade through the full MARS belief update math:**
   * Classify outcome (thesis_failure, execution_friction, market_halt, etc.)
   * Calculate magnitude-weighted delta: `clip(simulated_return / expected_risk, -2.0, +2.0)`
   * Update belief: `s_new = clip(s_current * decay + alpha * w(d) * delta(o), 0.15, 0.98)`
   * Build engram if pattern repeats successfully
   * Tag with regime context at time of trade
2. **Output:** A complete belief graph with trust scores, engrams, regime-conditional beliefs, and episodic memory — representing what the agent would have believed after living through months of trading

**What it produces:** An agent that wakes up with earned trust in specific capabilities, proven engrams for patterns it has "seen work," and scar tissue from simulated failures. It has beliefs. It may already be in AUTONOMOUS mode for capabilities where the simulated track record was strong enough.

**Analogy:** A trader who spent 6 months on a simulator before getting live capital. Not the same as real experience, but vastly better than theory alone.

### Type 3: Combined Backfill (Group C subset)

Knowledge AND experience backfill together. The agent has the right mental models AND simulated trading history. Theory plus practice. This is the best-case scenario for synthetic preparation.

### Type 4: Cold Start (Group C subset)

No backfill at all. The agent starts with the base MARS architecture, zero beliefs, zero engrams, and must discover everything through live trading. This is the control that reveals how much value backfill actually provides.

### Confidence Discount

All backfilled beliefs carry inflated uncertainty in the Beta posterior model:

```
Live belief:        Beta(successes + 1, failures + 1)
Knowledge backfill: No beliefs to discount — only context, no trust scores
Experience backfill: Beta(successes * 0.7 + 1, failures * 1.3 + 1)
```

The 30% haircut on experience backfill accounts for:

* Simulated adjudication is less reliable than live adjudication
* Historical signal corpus has survivorship bias
* Backfill can't simulate the market's reaction to the system's own trades
* Regime at time of backfill may not match current regime

The system learns over time how much to trust backfilled vs live-earned beliefs per domain. "Backfilled beliefs for energy signals are 85% as predictive as live-earned. Backfilled beliefs for political signals are 50% as predictive." This meta-learning is itself a belief in the graph.

### Experience Backfill: What Gets Simulated

For each historical signal, the backfill runner generates a complete simulated episode:

```json
{
  "signal": { "source": "reuters", "headline": "OPEC+ agrees surprise cut...", "timestamp": "2024-06-02T09:15:00Z" },
  "thesis": {
    "direction": "bullish_oil",
    "reasoning": "Supply cut exceeds consensus by 500k bpd. Market pricing only 200k cut. Edge: ~$3/barrel upside.",
    "conviction": 0.78,
    "kill_conditions": ["OPEC member signals non-compliance within 48hrs", "USD spikes >1.5% same day"]
  },
  "trade": {
    "capability": "options.bull_call_spread",
    "instrument": "USO",
    "structure": "Buy $75 call, sell $80 call, 30 DTE",
    "entry_price": 2.15,
    "max_risk": 215
  },
  "outcome": {
    "exit_price": 3.40,
    "return_pct": 58.1,
    "risk_adjusted_return": 1.16,
    "thesis_correct": true,
    "exit_reason": "profit_target",
    "holding_period_hours": 36
  },
  "regime_at_time": { "vol": "medium", "trend": "bullish", "risk": "on" },
  "belief_update": {
    "capability": "options.bull_call_spread",
    "context": "opec_supply_surprise",
    "delta": 1.16,
    "new_trust": 0.67
  }
}
```

Hundreds of these episodes, processed sequentially through the belief math, produce a belief graph with real statistical substance. The agent that inherits this graph knows not just "oil trades can work" but "oil bull call spreads after OPEC supply surprises in medium-vol bullish regimes have a 0.67 trust score based on 23 simulated episodes with 16 successes and 7 failures."

### Sub-Team Backfill

When a desk spawns a sub-team, the sub-team gets the relevant SLICE of the backfilled belief graph:

* Strait of Hormuz sub-team → shipping, oil, Middle East geopolitical subset
* Earnings sub-team → earnings signals, IV patterns, post-earnings move subset
* Filing anomaly sub-team → EDGAR filing patterns, insider transaction subset

Slicing is ontology-driven. The pack's entity/relationship graph determines which beliefs are relevant to which domain.

### The Backfill Runner (Go)

A dedicated binary (`cmd/backfill/main.go`) that:

1. Reads the historical signal corpus from PostgreSQL/blob storage
2. For each signal, queries historical market data from thinkorswim
3. Runs the full cognitive loop (scanner → research → prosecution → risk → execution simulation)
4. Scores against actual outcomes
5. Processes through the belief update math
6. Outputs a complete belief graph to Neo4j
7. Can be sliced by domain, capability, regime, and time period for mounting onto specific agents

The backfill runner is also the backtesting engine. Any strategy or pattern can be tested against historical data using the same mechanism. The difference between backfill and backtest is intent: backfill generates beliefs for agents; backtest generates performance metrics for humans.

---

## First Principles Seeding

Every desk and agent is seeded with foundational reasoning frameworks. These are NOT beliefs in the MARS sense. They are system prompts, reference documents, and reasoning substrates.

### Universal (All Desks)

* **Market microstructure** : order books, bid-ask spreads, market maker behavior, gamma hedging mechanics, options pinning at round strikes, expiration dynamics
* **Behavioral finance** : loss aversion, anchoring, herding, overreaction to narrative, underreaction to base rates, disposition effect
* **Risk management** : Kelly criterion, correlation spikes in crises, drawdown > return, worst loss always ahead, leverage kills
* **Historical case studies** (as reference, not beliefs): Ackman COVID trade, Soros Bank of England, LTCM collapse, 2010 flash crash, GameStop squeeze, SVB collapse

### Options Desks

* Black-Scholes and beyond, Greeks as exposure language, variance risk premium, IV skew mechanics, theta non-linearity, volatility surface dynamics, term structure
* Put-call parity, early exercise considerations, dividend impact, pin risk

### Macro Desks

* Fed transmission mechanism, yield curve inversion → recession lag, dollar wrecking ball dynamic, Soros reflexivity loops, carry trade mechanics, emerging market vulnerability to USD strength

### Corporate Desks

* Revenue recognition red flags, cash flow vs accrual accounting divergence, insider transaction patterns, quality indicators (ROIC, FCF yield, capital allocation), institutional ownership signals

### Geopolitical Desks

* Cascade mapping frameworks (action → reaction chains), sanctions mechanics, commodity dependency maps, alliance structures, historical conflict-to-market-impact patterns

---

## The A/B Test

### Design

## The Three-Group Experiment

All three groups run the full MARS belief architecture. The 12-test matrix and 500-cycle TAMI run already proved MARS outperforms raw execution on structured tasks. This is the next phase: testing which ORGANIZATIONAL DESIGN best exploits the belief architecture for trading.

### Group A — The Firm (20 desks × $25,000)

Full organizational units with group dynamics.

* **Structure:** Permanent trios — trader, analyst, researcher — per desk
* **MARS:** Full cognitive loop, belief accumulation, engrams, earned autonomy
* **Group memory:** Shared episodic memory within the trio, group-level engrams, compressed communication from repeated interaction
* **Sub-teams:** Can spawn temporary deep-dive teams (3-12 agents) for specific investigations
* **Domain coverage:** Broad — each desk covers a full information domain (geopolitical, macro, corporate, etc.)
* **Target:** 2% daily aggregate across all 20 desks
* **Backfill:** Half (10 desks) seeded with knowledge backfill (first principles, domain expertise, market structure). Half (10 desks) seeded with experience backfill (simulated trading episodes replayed through the belief graph — full thesis → execution → resolution cycles from historical data). Comparison reveals whether inherited knowledge or inherited experience produces better outcomes.
* **What it tests:** Does the firm model — trios arguing, shared context compounding, sub-teams going deep — produce alpha that individual agents can't?

### Group B — The Soloists (20 desks × $25,000)

Individual agents with deep beliefs but no group dynamics.

* **Structure:** One agent per desk. No trio. No conversation. No internal disagreement. The agent does its own research, analysis, and trading.
* **MARS:** Full cognitive loop, belief accumulation, engrams, earned autonomy — identical to Group A
* **No group memory:** Individual episodic memory only. No shared context. No compressed communication. No "the analyst disagrees" dynamic.
* **No sub-teams:** The agent works alone. Can use self-hosted LLMs for internal chain-of-thought but doesn't have persistent team members
* **Domain coverage:** Same domain distribution as Group A. For each Group A desk, a matched Group B desk in the same domain with the same approach
* **Target:** 2% daily aggregate across all 20 desks
* **Backfill:** Same split — 10 knowledge-backfilled, 10 experience-backfilled, matched to Group A pairs
* **What it tests:** Is the trio the magic, or is a single agent with deep beliefs and full MARS equally good? This isolates the value of organizational structure from the value of the belief architecture itself.

### Group C — The Hunters (200 micro-agents × $500)

Extreme niche specialization. Signature move discovery.

* **Structure:** Individual agents, each assigned a narrow niche
* **MARS:** Full cognitive loop, belief accumulation, engrams, earned autonomy
* **Niche constraint:** Each agent gets a domain narrow enough to force discovery of ONE repeatable pattern. Not "energy" — "SPY options 0-7 DTE." Not "biotech" — "biotech FDA advisory committee week vol plays." Not "macro" — "gold futures overnight session mean reversion."
* **Target:** 3% daily per agent ($15/day). Forces urgency. Can't spray trades — must find and execute one reliable play.
* **Capital:** $500 per agent. Can't diversify, can't absorb long losing streaks. Constraint eliminates everything except the purest, most repeatable edge.
* **Lifecycle:** Agents that can't find a profitable niche within 2 weeks get reassigned to a different niche by the CEO referee. Agents that find consistent profitability keep running and their signature move engrams get promoted to Group A/B desks for validation at scale.
* **Backfill split:**
  * 50 agents: knowledge backfill (first principles + domain expertise for their niche)
  * 50 agents: experience backfill (simulated trading episodes specific to their niche)
  * 50 agents: both knowledge AND experience backfill combined
  * 50 agents: cold start — no backfill, build beliefs entirely from live trading
* **What it tests:** Does constraint-forced specialization discover repeatable patterns that broad synthesis misses? Is alpha in depth of repetition rather than breadth of reasoning? Can the belief graph converge faster on narrow patterns? Does backfill type matter more for specialists than generalists?

### Niche Assignments for Group C (200 agents)

Each micro-agent gets one of these narrow niches (multiple agents per niche allows comparison):

**Options Niches (50 agents):**

* SPY 0-DTE options (scalping intraday premium)
* SPY weekly iron condors (specific DTE windows)
* QQQ straddles before tech earnings
* Single-stock covered calls on high-IV names
* VIX call spreads when VIX < 15
* Put selling on quality stocks after >5% drops
* Earnings straddles by sector (tech, financials, healthcare, consumer, industrial)
* LEAPS on beaten-down quality names
* Butterfly spreads at round-number strikes

**Equity Niches (40 agents):**

* Overnight gap reversals on large-caps
* Post-earnings drift (buy winners, short losers)
* Insider buying signals (Form 4 → buy within 48hrs)
* Ex-dividend capture strategies
* Index rebalance front-running (Russell, S&P additions/deletions)
* Sector rotation on PMI data releases
* Mean reversion on single stocks after 3+ day losing streaks

**Macro Niches (30 agents):**

* Gold overnight session (Asia/Europe hours)
* USD/JPY around BOJ communications
* Treasury futures around FOMC
* Oil futures on Wednesday EIA inventory data
* Copper as global growth proxy trade
* EUR/USD on ECB meeting days

**Event-Driven Niches (40 agents):**

* FDA binary events (approval/rejection date plays)
* FOMC day trades (pre-announcement positioning)
* CPI release day trades (10 minutes before to 1 hour after)
* Jobs report day trades
* Geopolitical escalation/de-escalation trades
* Earnings whisper number divergence plays

**Systematic Niches (40 agents):**

* Opening range breakout (first 30 min → direction for day)
* VWAP reversion on large-caps
* RSI oversold bounce plays on specific stocks
* Pairs trading within sectors (long strong / short weak)
* Calendar spread rolls (monthly maintenance plays)
* Theta harvesting on low-IV names (sell premium, collect time decay)

### Cross-Group Interactions

**Promotion pipeline:** When a Group C micro-agent discovers a reliable signature move (30+ iterations, >60% hit rate, positive expected value), its engram gets promoted to Group A and Group B desks operating in related domains. The desks can then execute the pattern at larger scale with higher confidence because it's been validated hundreds of times at micro scale.

**Belief sharing:** All three groups contribute to the Layer 1 (cross-agent) belief graph. Patterns discovered by Group C specialists enrich the engram library available to Group A firm desks and Group B soloists. The belief architecture ensures no trust transfers — only plans. Desks must still validate promoted patterns through their own execution.

**Regime signal:** Group C agents, because they're running the same narrow play daily, are extremely sensitive to regime changes. If 15 out of 20 Group C agents running options strategies suddenly start losing, that's an early warning signal for Group A and Group B that the vol regime has shifted — faster than any regime detection algorithm.

### What the Three-Group Experiment Answers

| Question                                                                           | Comparison                                                      |
| ---------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| Does organizational structure (trios) generate alpha beyond individual capability? | Group A vs Group B                                              |
| Does constraint-forced specialization find different alpha than broad synthesis?   | Group C vs Groups A+B (per-dollar basis)                        |
| Does the trio conversation justify its cost (3× the LLM calls)?                   | Group A Sharpe vs Group B Sharpe                                |
| Can micro-patterns scale when promoted to larger capital?                          | Group C engrams executed by Group A/B                           |
| Does inherited knowledge or inherited experience produce better outcomes?          | Knowledge-backfilled vs experience-backfilled within each group |
| Does cold start eventually catch up to backfilled agents?                          | Cold-start Group C vs backfilled Group C over time              |
| Where does alpha actually live — breadth or depth?                                | Portfolio attribution across all three groups                   |

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

### Layer 5: Periodic Belief Decay (Nuclear Option)

* Every quarter, all beliefs decay 10-15% across the entire system
* Every desk must re-earn trust
* Prevents slow accumulation of stale confidence from extended favorable regimes
* Same principle as rotating traders across desks

---

## LLM Architecture

### Self-Hosted (Unlimited, 24/7)

Running on Azure GPU VMs via vLLM or TGI:

**Qwen 2.5 72B (or Llama 3.3 70B) — "The Workhorse"**

* Thesis formation, evidence synthesis, cascade mapping
* Deep research, filing analysis, multi-source synthesis
* Group conversations between desk members
* 4× A100 80GB GPUs
* ~$10,500/month

**Qwen 2.5 7B (or Mistral 7B) — "The Scanner"**

* Signal scanning (is this tradeable?)
* News parsing and summarization
* Translation (all languages → English)
* Base rate lookup
* 2-3× T4 instances
* ~$1,600/month

### API (Pay-Per-Use, Surgical — 5% of calls)

**Claude Sonnet (via Anthropic API)**

* Prosecution / adversarial review
* Complex geopolitical synthesis requiring deep reasoning
* Final conviction scoring on high-value theses
* Meta-skeptic function (CEO)

**These are the highest-leverage LLM calls.** Quality matters most here. Three self-hosted models arguing produces good reasoning. But the final "why is this wrong?" before capital is committed needs the strongest available model.

### Why Self-Hosted for Group Conversations

The desk trios have multi-turn conversations — researcher presents findings, analyst contextualizes, trader stress-tests. Each conversation is 50-100 LLM calls. With 40 desks having continuous conversations plus sub-teams, that's millions of calls per day.

At API pricing: ~$100k+/month. Self-hosted: ~$12k/month, unlimited. The group conversation model is only economically viable with self-hosted inference.

Three Qwen 72B instances arguing with each other may produce better reasoning than one Claude Opus alone, because disagreements surface assumptions a single model never questions.

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
      wire.go                 # Fan-in coordinator
      news.go                 # RSS, webhooks, scrapers
      market.go               # thinkorswim streaming quotes
      options.go              # Options chain data, IV surfaces
      economic.go             # Scheduled releases (BLS, Fed, Treasury)
      filings.go              # EDGAR 10-K/10-Q/8-K/Form 4
      social.go               # Twitter, Reddit, Telegram
      alternative.go          # Satellite, AIS, flight tracking
      translate.go            # Multi-language translation (self-hosted)

    scanner/                  # Signal → tradeable opportunity detection
      scanner.go              # Main scanner loop (Qwen 7B, cheap, fast)
      momentum.go             # Price/volume breakouts
      volatility.go           # IV vs RV divergence
      event.go                # Upcoming catalyst detection
      anomaly.go              # Unusual options activity, insider trades
      macro.go                # Cross-market correlation breaks
      sentiment.go            # Crowd positioning, consensus shifts
      cascade.go              # Event cascade detection

    research/                 # Thesis formation (per desk)
      desk.go                 # Research orchestration
      fundamental.go          # Filing analysis, business quality
      technical.go            # Price action, levels, flows
      options_analysis.go     # Greeks, IV surface, skew analysis
      cascade.go              # Event cascade mapping (multi-layer depth)
      prosecutor.go           # Adversarial challenge (Claude Sonnet)
      structure.go            # Optimal instrument/trade structure selection
      conviction.go           # Final conviction scoring

    risk/                     # The Risk Desk (deterministic, no LLM)
      gate.go                 # Pre-trade validation
      portfolio.go            # Portfolio-level constraints
      greeks.go               # Options portfolio Greeks management
      correlation.go          # Cross-position and cross-desk correlation
      drawdown.go             # Drawdown monitoring and controls
      regime.go               # Regime detection and transition handling
      killswitch.go           # Emergency halt (portfolio, system, manual)
      tokens.go               # Capability token minting and validation

    execution/                # Order management
      manager.go              # Order lifecycle state machine
      schwab.go               # Schwab/thinkorswim API adapter
      options_orders.go       # Multi-leg options order construction
      futures_orders.go       # Futures order handling
      saga.go                 # Multi-leg saga orchestration
      idempotency.go          # Idempotency key management

    book/                     # Portfolio tracking
      portfolio.go            # Positions, exposure
      pnl.go                  # P&L calculation (realized + unrealized)
      greeks_book.go          # Portfolio Greeks (delta, gamma, theta, vega)
      mark.go                 # Real-time mark-to-market
      attribution.go          # Performance attribution (desk, domain, factor, time)
      factors.go              # Factor decomposition

    memory/                   # The Belief System (MARS core)
      belief.go               # Belief graph (Neo4j — Layer 1 + Layer 2)
      trust.go                # Trust update math (Beta posteriors)
      cascade.go              # Hierarchical + lateral belief cascade
      engram.go               # Engram storage, cross-desk aggregation
      episodic.go             # Trade episode storage (full lifecycle)
      semantic.go             # Pattern extraction across episodes
      procedural.go           # Evolved rules from observed patterns
      regime.go               # Regime-conditional belief partitioning
      backfill.go             # Synthetic belief generation from historical data

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
      selfhosted.go           # vLLM/TGI self-hosted client
      anthropic.go            # Claude API client
      router.go               # Route calls to appropriate model tier

    ab/                       # A/B test infrastructure
      experiment.go           # Experiment definition and tracking
      control.go              # Control group (no beliefs) desk wrapper
      treatment.go            # Treatment group (full MARS) desk wrapper
      analysis.go             # Statistical comparison of groups

  pkg/
    signal/                   # Signal types and interfaces
    schwab/                   # Schwab/thinkorswim API client library
    types/                    # Shared types

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
    signals := f.wire.Subscribe(ctx)  // fan-in all sources

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
    if err != nil || thesis.Conviction < d.minConviction { return }

    // Prosecution: adversarial challenge (Claude Sonnet)
    survived := d.prosecutor.Challenge(ctx, thesis, d.beliefs)
    if !survived { return }

    // Risk: deterministic check against pack policies
    order := thesis.ToOrder(d.trader)
    token, decision := d.risk.Check(ctx, order, d.book, f.portfolio)
    if !decision.Allowed { return }

    // Execute with capability token
    fill, err := d.execution.Submit(ctx, token, decision.AdjustedOrder)
    if err != nil { /* handle, possibly compensate */ return }

    // Book it
    d.book.OpenPosition(ctx, fill, thesis)

    // Memory: record episode start
    d.memory.RecordEntry(ctx, thesis, fill, d.ID)
}
```

---

## 115-Day Timeline

### Days 1-14: Foundation

* Azure GPU VMs provisioned, vLLM serving Qwen models 24/7
* PostgreSQL, Redis, Neo4j running on Azure
* Go project scaffolded with full directory structure
* thinkorswim API connected — can read markets, submit paper orders, get fills
* Basic Wire running (news RSS + thinkorswim market data streaming)
* Signal schema implemented
* Belief graph schema in Neo4j
* Pack structure defined (ontology, capabilities, policies, objectives)

**Deliverable:** Infrastructure running. Can query markets and LLMs are serving.

### Days 15-28: First Desks

* 5 desks stood up (one per major domain) with full trio
* Scanner agents running against market data
* Simple thesis formation (Qwen 72B)
* Prosecution layer working (Claude Sonnet)
* Risk gate implemented (position limits, daily loss, kill switch)
* First paper trades executing
* Book tracking P&L in real time
* Audit trail logging every decision
* Belief graph recording outcomes

**Deliverable:** 5 desks making paper trades. P&L visible. Beliefs accumulating.

### Days 29-42: Options + Backfill

* Options chain analysis working
* Multi-leg order construction (saga workflows)
* IV surface analysis, vol regime detection
* Full backfill runner built — processes AiFW historical corpus against market data
* Backfilled beliefs generated and mounted on desks
* Sub-team spawning mechanism working
* Neo4j knowledge graph building entity relationships
* Scale to 20 desks

**Deliverable:** Options trading active. Backfilled beliefs seeded. 20 desks running.

### Days 43-56: Full System + Three-Group Experiment

* Scale to full deployment: 40 desks (Group A + Group B) + 200 micro-agents (Group C)
* Three-group experiment infrastructure tracking all groups
* CEO referee operational (monitoring, correlation detection, allocation across all groups)
* Full multi-language Wire (translation pipeline, social signal, Telegram)
* Deep cascade mapping across domains
* Group conversation dynamics between Group A desk trios
* Regime detection working across all dimensions
* Group C micro-agents assigned to niches and running daily
* Backfill runner producing both knowledge and experience backfills
* Backfill variants distributed across agents per experimental design

**Deliverable:** Full 240-agent system running. Three-group experiment active. 24/7 operation.

### Days 57-70: Learning Kicks In

* Enough trade history for pattern extraction across all three groups
* Engrams forming for proven patterns
* Belief graph differentiating winning from losing approaches
* Regime-conditional beliefs accumulating
* Autonomous mode activating for high-trust capabilities
* CEO performing first capital reallocations based on performance data
* Group C micro-agents: first signature moves emerging — agents that found repeatable patterns visible in belief graph convergence
* Group C attrition: agents that couldn't find edge in their niche get reassigned to new niches
* First Group C engram promotions: validated micro-patterns offered to Group A/B desks
* Backfill comparison data accumulating: knowledge-backfilled vs experience-backfilled vs combined vs cold-start

**Deliverable:** Differentiation between groups beginning to show. Micro-agent signature moves emerging.

### Days 71-84: Hardening + Scale

* Prosecution gets harder (informed by accumulated failure patterns)
* Risk gates tightened based on observed drawdowns
* Factor decomposition detecting hidden concentrations
* Anti-overfitting layers all active
* Sub-teams deploying for deep investigations
* Performance attribution revealing which domains/approaches/timeframes work

**Deliverable:** System is self-tuning. Weak desks identified and capital reallocated.

### Days 85-100: Validation

* 30+ days of continuous operation with full three-group experiment data
* Statistical significance tests: Group A vs Group B vs Group C
* Per-desk, per-domain, per-approach, per-niche performance reports
* Belief graph quality analysis (are beliefs predictive? do backfilled beliefs help?)
* Engram hit rate analysis (do proven patterns actually work at scale?)
* Full factor decomposition of portfolio P&L
* Backfill comparison: knowledge vs experience vs combined vs cold-start — which produces best outcomes?
* Group C signature move catalog: which niches found repeatable edge?
* Cross-group promotion analysis: do micro-patterns scale when desks execute them?

**Deliverable:** The answer to "where does alpha live — in organizational depth, individual capability, or niche repetition?"

### Days 100-115: Decision Point

The data answers these questions:

1. **Does the trio structure justify its cost?** Group A vs Group B on risk-adjusted returns. If Group A wins, the group conversation and shared context compound into better reasoning. If Group B matches, individual agents with deep beliefs are sufficient and you save 2/3 of LLM costs.
2. **Does constraint-forced specialization find different alpha?** Group C per-dollar returns vs Groups A/B. If Group C's micro-agents produce better risk-adjusted returns per dollar allocated, the edge is in repetition, not synthesis.
3. **Which backfill type works?** Knowledge vs experience vs combined vs cold-start across all groups. This determines how every future agent gets seeded.
4. **Which domains produce alpha?** The CEO referee's attribution data reveals which information domains (geopolitical, macro, corporate, flows, tail, vol, sector, systematic) consistently generate profitable trades.
5. **Which niches have signature moves?** Group C's catalog of repeatable patterns — validated by hundreds of iterations — becomes a library of proven plays that can be scaled with real capital.

Based on answers:

* Reallocate capital toward winning organizational models
* Scale proven niches
* Kill dead domains
* Begin planning transition from paper to live with small real capital
* The system, the belief graph, the track record, and the signature move catalog become the product

The 115 days of data are the most comprehensive dataset ever collected on LLM-driven autonomous trading regardless of outcome. That data has value even if the system isn't profitable — it maps the frontier of what's possible.

---

## Infrastructure: Azure Deployment

### Compute

| Resource                      | Purpose                                                   | Est. Monthly Cost |
| ----------------------------- | --------------------------------------------------------- | ----------------- |
| 4× NC24ads_A100_v4           | Qwen 72B inference cluster (desk research + prosecution)  | ~$10,500          |
| 4× NC8as_T4_v3               | Qwen 7B scanner/translation/micro-agent reasoning         | ~$2,200           |
| 2× Standard_D8s_v5           | Go services (floor daemon, 280 agents), Redis, PostgreSQL | ~$700             |
| 1× Standard_E8s_v5           | Neo4j (belief graphs for 280 agents)                      | ~$500             |
| 1× Standard_D4s_v5           | Backfill runner (batch processing)                        | ~$300             |
| Blob Storage                  | Signal archive, episode storage, backfill corpus          | ~$300             |
| Azure Cache for Redis         | Hot state, signal queues, 280 agent states                | ~$500             |
| Azure Database for PostgreSQL | Book of record, audit trail, experiment tracking          | ~$500             |
| Anthropic API                 | Claude Sonnet for prosecution (~5% of calls)              | ~$3,000           |

**Total: ~$18,500/month**

With 280 agents (including 200 micro-agents that are lighter-weight than desk trios), the GPU cluster needs slightly more capacity for concurrent inference. The micro-agents run primarily on Qwen 7B (scanner-tier decisions), with Qwen 72B for thesis formation and Claude Sonnet only for prosecution on high-conviction trades. The 200 Group C agents collectively use less compute than the 40 Group A/B desks because their reasoning chains are shorter and more focused.

With $135k in credits over ~115 days (~3.8 months): $135k / 3.8 = ~$35.5k/month budget. Infrastructure uses ~52% of budget, leaving headroom for scaling up GPU instances during high-activity periods and Anthropic API costs for prosecution calls.

### Data Flow

```
Sources → Wire (Go goroutines) → Redis Streams → Scanner (Qwen 7B)
  → Filtered signals → Desk channels (Go channels per desk)
    → Research (Qwen 72B group conversation)
      → Prosecution (Claude Sonnet API)
        → Risk Gate (deterministic Go)
          → Execution (thinkorswim API)
            → Book (PostgreSQL)
              → Memory (Neo4j belief update)
                → Audit (PostgreSQL append-only log)
```

---

## Success Criteria

### 30-Day Forward Test

**Group A — The Firm (trios):**

| Metric                 | Target                           |
| ---------------------- | -------------------------------- |
| Aggregate daily return | ~2% (across 20 desks)            |
| Portfolio Sharpe ratio | > 2.0                            |
| Maximum drawdown       | < 10%                            |
| Win rate (per trade)   | > 52%                            |
| System uptime          | > 99%                            |
| Belief predictiveness  | Engram hit rate > base rate      |
| Autonomous mode trades | Outperform reasoning mode trades |

**Group B — The Soloists (individuals):**

| Metric                 | Target                |
| ---------------------- | --------------------- |
| Aggregate daily return | ~2% (across 20 desks) |
| Portfolio Sharpe ratio | > 2.0                 |
| Maximum drawdown       | < 10%                 |
| Win rate (per trade)   | > 52%                 |

**Group C — The Hunters (micro-agents):**

| Metric                                                         | Target                |
| -------------------------------------------------------------- | --------------------- |
| Per-agent daily return                                         | ~3% ($15/day on $500) |
| Agents finding signature moves (30+ iterations, >60% hit rate) | > 40 out of 200       |
| Signature move Sharpe                                          | > 2.5                 |
| Survival rate (still profitable at day 30)                     | > 25%                 |

**Cross-Group Comparisons:**

| Metric                           | Comparison                                                       |
| -------------------------------- | ---------------------------------------------------------------- |
| Risk-adjusted return per dollar  | Group A vs B vs C                                                |
| Group A vs Group B               | Statistically significant difference (p < 0.05)                  |
| Knowledge vs experience backfill | Measurable performance difference across groups                  |
| Cold-start convergence           | Days until cold-start agents match backfilled performance        |
| Promoted engram success rate     | Group C patterns executed by Group A/B vs their organic patterns |

### What Constitutes Proof

A month of consistent positive aggregate returns across all 240 agents, with full audit trail showing reasoning, beliefs, thesis formation, prosecution, and execution — against live market data with simulated execution. Plus statistical clarity on which organizational model, which backfill type, and which domains produce alpha.

That's not a backtest. That's a forward test with 240 concurrent experiments. That's unprecedented.

---

## Additional Ideas: Emergent Capabilities

### Tournament Mode

Every 2 weeks, the bottom 20% of Group C agents (by risk-adjusted return) get eliminated. Their capital gets redistributed to the top 20%. Surviving agents get slightly larger allocations. After 8 weeks, the 40-50 survivors are battle-tested specialists with proven signature moves and deep belief graphs. This creates evolutionary pressure beyond just the daily target — agents must be consistently good enough to survive periodic culling.

### Adversarial Desk

One special desk in Group A whose only job is to find trades that bet against the consensus of all other desks. When 80% of desks are bullish on something, the adversarial desk actively looks for the bear case and trades it. This desk will lose money most of the time. But during the moments when the entire system is wrong together — the correlated overconfidence scenario — this desk captures enormous alpha. It's structural insurance against institutional groupthink. The adversarial desk's beliefs track "when consensus is extreme, how often is the consensus wrong?"

### Cross-Pollination Sessions

Weekly scheduled sessions where the CEO referee broadcasts the top 5 performing Group C signature moves to all Group A and Group B desks. Desks can choose to adopt, test, or ignore. If a desk adopts a micro-pattern and it works at larger scale, both the originating micro-agent and the adopting desk get positive belief updates. This creates a formal mechanism for bottom-up pattern discovery to influence top-down strategy.

### Time-of-Day Specialization

Some Group C agents get assigned not just a domain niche but a TIME niche. "Asian session gold futures" or "first 30 minutes of US market open" or "European close → US pre-market overlap." Time-of-day patterns are among the most robust in markets because they're driven by structural factors (when institutions trade, when data releases happen, when liquidity shifts). Agents that specialize in time windows might find the most durable signature moves.

### Belief Graph Visualization Dashboard

A real-time dashboard showing:

* Every agent's belief graph as a heatmap (capabilities × confidence)
* Group C signature move discovery rate over time
* Cross-group engram promotion flow
* Regime detection state and how many agents are in AUTONOMOUS vs REASONING
* Factor decomposition pie chart showing portfolio concentration
* Backfill type comparison curves (knowledge vs experience vs combined vs cold-start)

This isn't just monitoring — it's the research output. The visualization of 240 agents' belief evolution over 115 days is itself a novel dataset about how LLM-based decision systems learn.

### Shadow Mode for New Strategies

Before any new strategy goes live (even on paper), it runs in shadow mode for 1 week — generating trade signals but not executing. The signals are scored against what would have happened. This creates a pre-validation layer: strategies must demonstrate edge in shadow mode before they're allowed to consume capital. The belief graph for shadow-mode strategies accumulates without risking capital.

### Desk DNA Replication

When a desk is performing exceptionally well, the CEO referee can "replicate" it — spawn a new desk with the same first principles, same backfill, same domain, but a fresh belief graph. The replica starts from the same seed but diverges as it encounters different market conditions. If the replica also performs well, the edge is in the setup. If it doesn't, the original desk's success may have been path-dependent (lucky early trades that shaped beliefs in a favorable direction). This is the ultimate overfitting test at the organizational level.

---

## Appendix A: MARS Component Mapping

| MARS Concept          | Trading Floor Implementation                                        |
| --------------------- | ------------------------------------------------------------------- |
| Intent                | Trade signal                                                        |
| Capability            | Trade structure (equity.long, options.straddle, etc.)               |
| Agent                 | Strategy archetype / desk seat                                      |
| Pack                  | Trading pack (ontology, capabilities, policies, objectives)         |
| Belief graph          | Per-desk trust in capabilities × contexts × regimes               |
| Engram                | Proven trading pattern with success/failure counts                  |
| Gap detector          | Market regime detection + position health monitoring                |
| Policy engine         | Risk desk (deterministic constraints)                               |
| Cognitive loop        | THINK → REMEMBER → PLAN → CHECK → ROUTE → ACT → LEARN         |
| Capability token      | Pre-trade authorization with constraints                            |
| Tool Gateway          | Risk gate + order validator                                         |
| Manager agent         | CEO referee                                                         |
| Agent pool            | 240 agents: 60 desk seats (Groups A+B) + 200 micro-agents (Group C) |
| Layer 1 (cloud)       | Cross-agent proven patterns (all groups contribute)                 |
| Layer 2 (org)         | Per-desk / per-agent earned beliefs                                 |
| Adjudication          | Trade resolution + thesis validation                                |
| Workflow orchestrator | Multi-leg order saga                                                |
| Thompson Sampling     | Capital allocation across desks and agents                          |

## Appendix B: Agent Census

| Group             | Structure            | Agents        | Capital/Agent            | Total Capital        | Target                                                  | Backfill Variants |
| ----------------- | -------------------- | ------------- | ------------------------ | -------------------- | ------------------------------------------------------- | ----------------- |
| A — The Firm     | 20 desks × 3 (trio) | 60            | $25,000/desk  | $500,000 | 2%/day aggregate     | 10 knowledge, 10 experience                             |                   |
| B — The Soloists | 20 desks × 1        | 20            | $25,000/desk  | $500,000 | 2%/day aggregate     | 10 knowledge, 10 experience                             |                   |
| C — The Hunters  | 200 individuals      | 200           | $500/agent    | $100,000 | 3%/day per agent     | 50 knowledge, 50 experience, 50 combined, 50 cold-start |                   |
| **Total**   |                      | **280** |                          | **$1,100,000** |                                                         |                   |

Plus temporary sub-teams spawned by Group A desks: potentially 100-500 additional agents at peak activity.

---

*This document is the build plan. Everything here is grounded in the MARS experimental results (12-test matrix, 500-cycle TAMI run, 25-paper research corpus) and adapted for the highest-stakes application of autonomous decision-making under uncertainty: markets.*

*The prior research proved the belief architecture's superiority on structured tasks. All three experimental groups run the full MARS architecture. The question isn't whether MARS works — it does. The question is which organizational design best exploits it: the firm, the soloist, or the hunter.*

*240 concurrent agents. 115 days. $1.1M paper trading account. Every trade, every belief, every reasoning chain logged. The most comprehensive experiment in LLM-driven autonomous trading ever conducted.*
