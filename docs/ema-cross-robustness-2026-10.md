# EMA20/50 Robustness, Statistical Confidence & Cost Reality — XAUUSD

**Date:** 2026-10-23 · **Companion to:** `ema-cross-evaluation-2026-10.md`

Phase 3 question: is the earlier "no demonstrated edge" conclusion robust?
It is — and it survives every additional probe. **Recommendation: retain
the strategy as a benchmark; do not promote it to live validation.**

## 1. Frozen benchmark (verified against stored config)

`ema-cross` v1.0.0 — fast=20, slow=50, warmup=150, 1h, positionSize=10%,
commission=0.02%, slippage=0.01%, capital=100,000, Tiingo `xauusd`.
Periods: E=`2020-01-03→2022-12-31`, V=`2023→2024`, OOS=`2025→2026-10-23`
(**now observed** — no longer untouched). Dataset
`tiingo:xauusd:1h@sha256:b533a392…`, 35,888 bars, 2020-01-02→2026-10-23.

## 2. Trade concentration (full-window 1h run `0c1c2e38`, 348 trades)

| Diagnostic | Value |
|---|---|
| Net P&L | +6,363 (6.4%, DD 2.97%) |
| Top-1 trade | +1,687 = **27% of net** |
| Top-3 trades | +3,841 = **60% of net** |
| Net ex-top-3 | +2,523 |
| Winners / losers / break-even | 113 / 235 / 0 |
| Net by year | 2020 +1,552 · 2021 −351 · 2022 −310 · 2023 +673 · 2024 +1,376 · **2025 +4,151 (65%)** · 2026 −728 |

Concentration is real but milder than period-split suggested: two-thirds of
the profit is one trend year (2025) and three trades. Removing top-3 is a
diagnostic, not an alternative strategy.

## 3. Block bootstrap (uncertainty quantification)

- Data: ordered 348-trade sequence, exit-time order.
- Method: **circular block bootstrap**, block length L=10 (trade clusters
  for a 30%-WR trend follower are serially dependent; iid would understate
  spread — reported for comparison), B=10,000 resamples, seed=20261023.

| Quantity | Observed | 95% bootstrap CI |
|---|---|---|
| Total net P&L | +6,363 | **[−821, +13,915]** |
| Mean trade | +18.29 | **[−2.36, +39.99]** |
| Trade-path maxDD | 2.97% | [1.01%, 4.23%] |
| P(resampled net ≤ 0) | — | **4.3%** |

The confidence interval **includes zero** — at conventional thresholds the
run cannot reject "no edge". iid resampling (dependence destroyed) gives
the same broad picture (P≈3.5%), so the conclusion isn't an artifact of
block choice; the effective sample is simply too small and too
concentrated for significance.

## 4. Cross-timeframe check — 4h & 1d, same fixed config (EXPLORATORY)

Different timeframes = different effective horizons — related variants,
not interchangeable tests. 2025–26 already observed → these are
descriptive only, all `cfg` costs, 10% sizing vs matched B&H-10%:

| TF | E net (T) | V net (T) | OOS net (T) | FULL net (T) |
|---|---|---|---|---|
| 1h | +1.0% (135) | +1.9% (108) | +3.3% (105) | +6.4% (348) |
| 4h | +2.6% (28) | +1.9% (31) | +2.4% (28) | +7.5% (87) |
| 1d | +0.4% (5) | +3.2% (3) | +6.6% (2) | +10.5% (10) |
| B&H-10% | +1.9% | +4.3% | +5.2% | **+16.1%** |

- **4h** looks the most attractive profile (positive all periods, survives
  10× costs at +0.8–1.1%) but still trails B&H in V and OOS; only E beats it.
- **1d**: 2–5 trades per period — **statistically meaningless**; full-run
  +10.5% on 10 trades can't support a claim.
- The 4h/1d datasets have `complete` coverage, same provider/fingerprint
  family. **Defect found and fixed during this check**: `Range` warmup was
  returning the boundary bar twice when `from` landed exactly on a stored
  4h bar (`TailCandles` inclusive `<=` + window `>=`) → engine's
  strictly-ascending guard correctly failed the run. Fixed in `db.go`
  (fetch warmup+1, drop boundary) + regression test. No candle data was
  ever duplicated in storage — the bug was in the read path only.

## 5. Cost reality — what we now know and don't

**Empirical evidence collected (prospective, not historical):**
`quote_observations` table + recorder on the live Tiingo stream — distinct
bid/ask events only, staleness-flagged. First ~4 min: **626 observations,
mean spread ≈ 0.25 bp** (≈$0.10 on $3,998), 0 stale rows.

- Tiingo's FX feed is a *consolidated indicative* feed — its ~0.25bp
  top-of-book spread is **not** a retail-executable spread. Real
  all-in cost for retail gold is typically an order of magnitude larger —
  untestable without broker data.
- The sensitivity grid stands: at **5×** configured costs only OOS
  survives on 1h; at **10×** all 1h/4h/1d E-period results are positive
  only on 4h/1d (fewer trades amortize costs less).
- **Honest statement of record**: every historical result's costs are
  assumptions; the recorder only builds *forward-looking* evidence.

## 6. Forward-validation protocol (for any future promotion)

Frozen before any prospective test:

- **Config**: `ema-cross` v1.0.0, 20/50, 1h, 10% sizing, engine v2.1.0 —
  hash-frozen; any change = new version + new protocol.
- **Data**: `market_candles` Tiingo 1h only; fingerprint pinned at test
  start; gaps disclosed.
- **Signals/fills**: hypothetical only — signal at bar close → next-bar
  open; recorded via the existing engine against incoming candles.
- **Costs**: `quote_observations` supplies realized spread distribution;
  hypothetical fills priced at recorded ask (buy) / bid (sell), not mid.
  Missing-observation bars → trade flagged `no_cost_data`, never filled at
  a made-up spread.
- **Duration**: ≥ 6 months continuous or ≥ 60 hypothetical trades,
  whichever is later; minimum 90% quote-coverage.
- **Metrics**: net P&L, trade-path maxDD, trade count, WR, PF vs
  frozen-configured-cost backtest of the same period; decision criterion:
  prospective net ≥ 0 AND PF ≥ 1.1 AND no material slippage/spread regime
  shift vs assumptions.
- **Invalidates the test**: quote-feed outage >24h, candle-coverage gap
  inside the window, engine version drift, parameter edits mid-test.
- The 2025–26 period is **already observed** — it can never again serve as
  untouched OOS for this strategy family.

## 7. Verdict

- **Concentrated?** Yes — 65% of net from 2025; top-3 trades = 60%.
- **Uncertainty?** Large — bootstrap CI on net crosses zero; P(net≤0)≈4%.
- **Same behavior across TFs?** Directionally yes (trend-capture in trend
  years, bleed in chop); 4h is the least-bad variant but still
  sub-baseline; 1d is untestable at n=10.
- **Cost dependence?** Decisive — the entire margin sits within ~5× of
  configured assumptions; real spreads are unknown but almost certainly
  wider than Tiingo's 0.25bp indicative feed.
- **Worth prospective testing?** Only as a **benchmark/control**, not as a
  candidate — the observed CI, concentration, and cost fragility give no
  evidence of edge to validate forward.
- **Frozen config (if run as control)**: exactly the §1 benchmark.

**Files:** `docs/ema-cross-robustness-2026-10.md` (this), quote recorder
(`service/quoterecord.go`, `postgres/quote_obs.go`, migration 000011,
`quoterecord_test.go`), warmup-boundary fix + regression test
(`market/db.go`, `db_test.go`). The recorder's first build compared
pointer addresses in its dedupe key — caught by unit test and fixed to a
value-comparable key; early rows remain valid observations (each was a
real delivered quote) but pre-fix identical redeliveries were also
persisted. Post-fix verified: 291 inserts = 291 distinct events.
All runs uncommitted, reproducible via run IDs in the Phase-2 report +
`FULL-1h=0c1c2e38`, `FULL-4h`, `FULL-1d` (see `backtest_runs`).
