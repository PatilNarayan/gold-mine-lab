# EMA Crossover Strategy Evaluation — XAUUSD 1h (Tiingo)

**Date:** 2026-10-23 · **Evaluator:** quantitative audit · **Data:** provider-backed `market_candles` store

This report evaluates whether the existing `ema-cross` strategy shows a
repeatable edge. No parameters were tuned; the production defaults were
evaluated as shipped. **Result: no demonstrated edge — the strategy
underperforms matched-exposure buy-and-hold in every period tested.**

## 1. Configuration under test

| Parameter | Value |
|---|---|
| Strategy | `ema-cross` v1.0.0 (`strategy.NewEMACross`) |
| fast / slow EMA | 20 / 50 (defaults — `parameters: {}`) |
| Warm-up | 150 bars (3 × slow_period, before first evaluated bar) |
| Entry | fast crosses ABOVE slow at bar *i* close → BUY filled at bar *i+1* **open** ×(1+slip) |
| Exit | fast crosses BELOW slow → SELL filled at next bar **open** ×(1−slip); residual position liquidated at final close |
| Sizing | `equity × 10% / fill` per trade, single long position only |
| Costs (configured) | commission 0.02% of notional per leg, slippage 0.01% adverse per fill |
| Capital | 100,000 USD |

## 2. Dataset provenance

| | |
|---|---|
| Provider | `tiingo` (REST history → `market_candles`) |
| Symbol / TF | `xauusd` / `1h` |
| Coverage | `complete` — 35,888 bars, 2020-01-02 → 2026-10-23 (Kyiv civil-time labels) |
| Fingerprint | `tiingo:xauusd:1h@sha256:b533a3921ad82f96f9d33068526514a75a79fdf86fcea37e55d4a3e89db02ed0` |
| Gaps | none fabricated — weekend/holiday closures are absent bars, not filled |

All 23 runs record `data_provider=tiingo`, `data_candles=35888`, and the
same fingerprint. Legacy CSV runs are excluded from every figure below.

## 3. Evaluation protocol

Fixed **before** computing results (calendar splits, not chosen for outcome):

| Period | Role | Range |
|---|---|---|
| E | Exploratory | 2020-01-03 → 2022-12-31 |
| V | Validation | 2023-01-01 → 2024-12-31 |
| OOS | Untouched out-of-sample | 2025-01-01 → 2026-10-23 |

Cost scenarios: `cfg` = configured (0.02%+0.01%), `zero` = diagnostic upper
bound (**unrealistic** — mid-price candles are not executable prices),
`5x` / `10x` = stress multiples of configured costs. The strategy's own
10% sizing is preserved; the buy-and-hold baseline deploys **10% of capital**
(+90% cash) with the same per-leg cost model — matched capital-at-risk, not
leverage-adjusted.

## 4. Results — strategy vs. baselines (configured costs)

| Period | Strategy net | Strategy DD | Trades | WR | PF | B&H-10% net | B&H DD | Cash |
|---|---|---|---|---|---|---|---|---|
| E (2020–22) | **+963 (1.0%)** | 1.93% | 135 | 30% | 1.17 | +1,923 (1.9%) | 2.86% | 0 |
| V (2023–24) | **+1,911 (1.9%)** | 1.59% | 108 | 31% | 1.43 | +4,345 (4.3%) | 1.30% | 0 |
| OOS (2025–26) | **+3,305 (3.3%)** | 2.97% | 105 | 37% | 1.57 | +5,214 (5.2%) | 5.48% | 0 |

**Read:** positive but *below buy-and-hold on the same 10% exposure in all
three periods*. The OOS return (3.3% over ~22 months at 10% deployment)
trails passive 5.2%; its drawdown (2.97%) is lower than B&H's (5.48%) —
the strategy trades less risk for less return, it does not add alpha.

## 5. Cost sensitivity

| Period | zero (unrealistic) | cfg | 5× | 10× |
|---|---|---|---|---|
| E | +1,785 | +963 | −2,257 | −6,135 |
| V | +2,575 | +1,911 | −699 | −3,865 |
| OOS | +3,960 | +3,305 | +730 | −2,396 |

**The margin is thin and cost-dominated**: at 5× configured costs only OOS
survives (+0.7%); at 10× everything is negative. Mid-price candles mean
actual spread cost is unknown — if real round-trip costs exceed ~5× the
configured values, the strategy is a net loser in every period. `zero`
shows the theoretical ceiling is already low (1.8–4.0% multi-year).

## 6. Per-year consistency (configured costs)

| Year | Net | DD | Trades | WR | PF |
|---|---|---|---|---|---|
| 2020 | +1,552 | 0.74% | 17 | 35% | 3.33 |
| 2021 | −266 | 0.92% | 59 | 32% | 0.88 |
| 2022 | −159 | 1.93% | 59 | 27% | 0.94 |
| 2023 | +557 | 1.59% | 51 | 29% | 1.27 |
| 2024 | +1,347 | 1.02% | 57 | 32% | 1.57 |
| 2025 | +4,013 | 0.81% | 49 | 53% | 3.41 |
| 2026 (partial) | −680 | 2.97% | 56 | 23% | 0.83 |

4 of 7 years positive; the aggregate is carried by **2025** (+4,013 — the
strong trending year) and the tiny 17-trade 2020. 2021–2022 and 2026 are
net-negative — no year-by-year consistency.

## 7. Trade concentration (configured costs)

| Period | Net | Top-1 trade | Top-3 trades | Net ex-top-3 |
|---|---|---|---|---|
| E | +963 | +1,687 (175%) | +2,786 | **−1,824** |
| V | +1,911 | +722 (38%) | +2,084 | **−173** |
| OOS | +3,305 | +1,064 (32%) | +2,850 | +456 |

Classic trend-following shape (win rate ~30%, few large winners). In E the
entire profit is one trade; in V the strategy is flat-to-negative without
its top three. Only OOS stays positive without them — the least fragile
window, but still not better than buy-and-hold.

## 8. Parameter sensitivity (exploratory label, E period only)

One-parameter-at-a-time, small predefined grid — **not** a search for the
best pair:

| Config | Net | DD | Trades | PF |
|---|---|---|---|---|
| f20/s50 (base) | +963 | 1.93% | 135 | 1.17 |
| f15/s50 | +927 | 2.07% | 152 | 1.16 |
| f25/s50 | +644 | 2.14% | 119 | 1.12 |
| f20/s40 | +1,021 | 2.11% | 145 | 1.17 |
| f20/s60 | +937 | 2.01% | 126 | 1.18 |

Nearby parameters cluster around the same ~1% outcome — the result is not a
knife-edge parameter artifact, but the neighborhood offers no better
profile either.

## 9. Reproducibility

Same request twice (earlier audit) → identical trades/metrics (only row
UUIDs differ). Every run below re-executes deterministically.

## 10. Run IDs (all `tiingo`, fp `tiingo:xauusd:1h@sha256:b533a392…`)

| Tag | Run ID |
|---|---|
| E-cfg / E-zero / E-5x / E-10x | c9ccd0a0…944 / 81acc260…238 / 4881e9fb…c3d / 4199cb8b…49b |
| V-cfg / V-zero / V-5x / V-10x | 38b2837a…187 / 6591fb64…7bc / 463380e0…c19 / 62e03eb1…079 |
| OOS-cfg / -zero / -5x / -10x | b994e246…f9f / 1a6df90b…e12 / aa82d786…a24 / 2f59d5d8…994 |
| yr2020…yr2026 | 9bd0d370…893 / d1bb75c0…316 / bee3dcb0…14f / 087bb471…133 / cf8f999d…453 / 0ae5baaf…863a / f855217c…60bf |
| E-sens f15/f25/s40/s60 | 417066a2…c09 / 32daac76…c05 / 1235a152…ee0 / 66ab9c34…615 |

## 11. Limitations

- **Costs are assumed, not measured**: mid-price candles; no historical
  spread/execution data. Anything above ~5× configured cost assumptions
  erases the result.
- **10% sizing mutes everything**: absolute returns and DDs are small;
  comparisons are at *matched* exposure — a 100%-deployed B&H would show
  ~10× both figures.
- **Long-only, no stops/scaling**: the strategy can't defend drawdowns or
  express conviction.
- **Coverage**: Tiingo floor 2020-01-01 — no earlier regime (2019 risk-off
  gold, 2008 crisis) exists in the dataset.
- **Sample size**: ~350 trades total over ~7 years — adequate for a rough
  read, thin for tight confidence intervals.
- **No Sharpe**: engine deliberately produces none (mixed-frequency bars).

## 12. Conclusions (evidence, not optimism)

- **Does it beat the baseline in the untouched window?** No — OOS +3.3%
  vs. matched-exposure B&H +5.2% (DD 2.97% vs 5.48% — less risk, less
  return, no alpha).
- **Positive under configured costs?** Yes in all three periods — but thin
  (0.7–3.3% multi-year) and sub-baseline.
- **Sensitive to higher costs?** Severely — negative everywhere at 10×,
  nearly so at 5×.
- **Consistent across periods?** No — 3 of 7 years negative; aggregate
  carried by 2025.
- **Trades?** ~350 total; each period 105–135.
- **Largest DDs:** strategy ≤3.0% (10% sizing), B&H ≤5.5%.
- **Verdict:** no repeatable edge demonstrated. The strategy deserves
  further research only as a *risk-managed overlay* concept (it did cut
  OOS drawdown) — not as a return engine in current form.
- **Before paper trading:** measured bid/ask cost model, ≥2× trades on
  independent data, a parameter-mine-resistant regime check (sub-period
  permutation/bootstrap), and a demonstrable reason to expect the 2025
  regime to persist.
