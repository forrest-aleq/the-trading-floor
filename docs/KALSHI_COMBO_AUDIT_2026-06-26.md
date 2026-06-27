# Kalshi MVE Combo Audit - Verified Findings and Fixes

**Date:** 2026-06-26  
**Scope:** Live Kalshi multivariate event (`KXMVE...`) wrapper fills, code path, and deployed runtime behavior.  
**Method:** Read-only Kalshi account/API inspection, repo audit, official Kalshi API docs, local runtime logs, and unit/integration tests. No orders were placed, cancelled, or modified during the audit/fix pass.

Official API references used:

- `GET /markets`: https://docs.kalshi.com/api-reference/market/get-markets
- `GET /series/{series_ticker}/markets/{ticker}/candlesticks`: https://docs.kalshi.com/api-reference/market/get-market-candlesticks

## Executive Verdict

The original critique was directionally correct: the system bought Kalshi MVE combo wrappers without comparing the wrapper price to the cost of replicating the same legs directly. That is the structural leak.

The original report also had two important problems:

1. Some account numbers were stale by the time the fix landed.
2. The claim that an uncommitted combo builder must be bypassing audited code was not proven and was likely wrong. The committed/deployed path could treat a `KXMVE...` wrapper as a normal Kalshi ticker and trade it as a single market.

The fix therefore had to be layered:

- Exclude MVE wrappers at the Kalshi market feed source.
- Reject MVE wrapper tickers in the scanner fast path.
- Reject MVE wrapper tickers in the mapper by default.
- If wrappers are ever deliberately re-enabled, require a live leg-product fair-value gate before any order can be submitted.

## Verified Account Ground Truth

Latest verified account/accounting facts during this investigation:

| Metric | Verified value |
|---|---:|
| Live MVE wrapper fills | 21 |
| Total paid | about $50.59 |
| Kalshi cash | about $0.0034 |
| Kalshi portfolio value after additional settlement marks | about $6.85 |
| Settled combos observed | 17 |
| Profitable settled combos | 0 |
| Realized pre-fee P&L observed | about -$40.50 |
| Instrument family | `KXMVESPORTSMULTIGAMEEXTENDED...` and `KXMVECROSSCATEGORY...` wrappers |

These were live real-money Kalshi fills, not dry-run fills.

The exact settled/open split can continue changing as the remaining wrappers settle, so this document treats those figures as investigation-time facts rather than permanent state.

## Core Economic Finding

The MVE wrapper price was materially above the independently replicable leg value.

The reconstructed book-level relationship was:

| Measure | Value |
|---|---:|
| Total combo cost | $50.592 |
| Reconstructed direct-leg value | $27.621 |
| Dollar overpay | $22.971 |
| Over fair value | about 83.17% |

That is enough to reject the strategy as implemented. The sample's 0-for-settled outcome is not the proof by itself; longshot combo tickets can naturally settle 0-for-many. The proof is that the system paid far more than the same payoff was worth when reconstructed from the selected legs.

For cross-game independent legs, direct leg product is the right baseline. For true same-game correlated legs, product-of-legs is not sufficient because correlation can make the joint probability higher than the independent product. That is why the correct future strategy is not "never same-game combos." It is "never buy any combo without comparing price to a leg-product baseline and, for same-game structures, a correlation-adjusted joint estimate."

## Classification Caveat

The earlier "20 of 21 cross-game, 0 same-game" statement was true only under a raw `event_ticker` grouping. That is useful but not fully sufficient.

Kalshi can represent economically related props with different event tickers. A proper same-match classifier needs a canonical game key based on participants, league, start time, and market metadata. Without that classifier, "same-game" vs "cross-game" is only approximate.

The conclusion still stands because the observed book paid an extreme wrapper markup and lacked any fair-value comparison. The classifier caveat matters for future alpha design, not for defending the old execution path.

## Actual Code Path

The actual failure mode was simpler than "a hidden combo builder went rogue."

Observed committed/deployed path:

1. `internal/wire/feeds/kalshi.go` fetched open Kalshi markets. Kalshi's default market listing behavior includes MVE wrappers unless `mve_filter=exclude` is supplied.
2. `internal/scanner/engine.go` accepted raw prediction-market inventory through the Kalshi market-discovery path.
3. `internal/research/desk.go` normalized Kalshi theses to `structure="single"` with empty legs.
4. `internal/execution/kalshi/mapper.go` accepted any `KX...` ticker, including `KXMVE...`, and mapped it as a normal single Kalshi order.
5. `internal/firm/desk.go` routed Kalshi execution through the Kalshi executor path, not the IBKR broker path.

So the bug was not that the model invented a multi-leg order object. The bug was that a multivariate wrapper ticker is itself a single tradable Kalshi market unless the system explicitly treats `KXMVE...` as dangerous.

## Fixes Shipped

### `1a42c0e` - Block unsafe Kalshi MVE wrappers

Added:

- `internal/execution/kalshi/mve.go`
- `internal/execution/kalshi/mve_test.go`
- `Market.MVECollectionTicker`
- `Market.MVESelectedLegs`

Behavior:

- `KXMVE...` tickers are blocked by default.
- The override is intentionally explicit and ugly: `KALSHI_UNSAFE_ALLOW_MVE_WRAPPERS=true`.
- The feed, scanner, and mapper all reject/block wrappers by default.

Tests cover:

- MVE ticker detection.
- Feed skip behavior.
- Scanner rejection reason `kalshi_mve_wrapper_blocked`.
- Mapper rejection.
- Raw `mve_selected_legs` preservation.

### `17a01cc` - Add Kalshi MVE fair-value gate

Added:

- `internal/execution/kalshi/mve_fair_value.go`
- `Client.GetMarket(ctx, ticker)`
- `MappedOrder.MVEFairValue`

Behavior if wrappers are explicitly re-enabled:

- Fetch the wrapper market.
- Read `mve_selected_legs`.
- Fetch each selected leg market.
- Compute selected-leg fair value from live side-specific asks.
- Reject if leg count exceeds `KALSHI_MVE_MAX_LEGS`, default `3`.
- Reject if `combo_price > fair * (1 + KALSHI_MVE_MAX_MARKUP)`, default `0.03`.

Tests cover:

- Rejecting a four-leg example where wrapper price is about `0.232` and live leg product is about `0.1145`.
- Allowing the same structure when wrapper price is within the fair-value threshold.
- Rejecting when leg count exceeds the configured cap.

### `38875b7` - Exclude Kalshi MVE markets at feed source

Added:

- `Client.GetMarketsWithMVEFilter(ctx, status, limit, cursor, mveFilter)`
- Feed-side use of `mve_filter=exclude` unless unsafe MVE mode is explicitly enabled.

Why this mattered:

- Simply skipping wrappers after fetch was insufficient because Kalshi's open-market pages were dominated by MVE wrappers.
- The feed could burn through its pagination budget on wrappers and starve normal markets.
- Official Kalshi docs show `mve_filter` supports excluding MVE markets at the API source.

Test coverage asserts that `mve_filter=exclude` is sent.

## Runtime Verification

After the fixes:

- The app was rebuilt and relaunched through `./scripts/install-launchd.sh`.
- Launchd label `com.hnic.trading-floor` was running.
- Runtime binary changed after rebuild.
- `KALSHI_UNSAFE_ALLOW_MVE_WRAPPERS` was absent, so wrapper blocking was active.
- Runtime logs showed Kalshi feed emissions for normal non-MVE tickers.
- No fresh `KXMVE...` Kalshi signals were observed after the final restart check.
- The Kalshi journal did not grow after the fix; last observed entry was pre-fix.
- Kalshi new-entry work was paused because available Kalshi cash/effective order risk was zero.
- IBKR/TWS sync recovered and reported connected/synced. HMDS farm warnings remained ordinary IBKR data-farm noise, not the Kalshi combo issue.

## What This Fix Does Not Yet Solve

This pass stops the dangerous wrapper path. It does not claim the whole strategy is now profitable.

Remaining high-priority work:

1. Inspect and cancel/resolve any old resting Kalshi orders before adding cash.
2. Persist structured telemetry for rejected MVE fair-value reports, including `{combo_price, fair_price, markup, leg_count, reason}`.
3. Build a canonical same-match classifier using teams, league, start time, and market metadata rather than raw `event_ticker`.
4. Fix open-combo mark-to-market if MVE wrappers are ever displayed again: mark all-or-nothing wrappers by joint value, not by summing leg marks.
5. Require structured sports availability evidence for player props. LLM text should not satisfy player-active or starter gates.
6. Add a general model-vs-market divergence rule: if the model estimate is far above implied price, require evidence explaining what the market is missing.
7. Reduce Kalshi order churn. The old behavior produced too many canceled limit orders relative to fills.

## Operational Rule Going Forward

No Kalshi combo wrapper order may reach execution unless all of the following are true:

1. `KALSHI_UNSAFE_ALLOW_MVE_WRAPPERS=true` is deliberately set.
2. The wrapper has decoded `mve_selected_legs`.
3. Every leg has a live quote.
4. Leg count is within the configured cap.
5. The wrapper price is within the configured markup over fair value.
6. Same-match structures have a separate correlation-adjusted joint estimate if the strategy depends on correlation.

Default production posture is stricter: exclude and block all `KXMVE...` wrappers.

## Bottom Line

The original "markup tax" thesis was real. The exact report needed cleanup, and the code-path diagnosis needed correction.

The system is now protected against accidental Kalshi MVE wrapper trading by default, and the deliberately re-enabled path has a live fair-value gate. Tests pass, and runtime was redeployed with wrapper blocking active.
