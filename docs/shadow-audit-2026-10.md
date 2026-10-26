# Shadow Session #8 — Decision-Audit — Phase 10

**Date:** 2026-10-26/27 · **Audit verdict:** 1 missing-evidence defect found & fixed; the zero-order state is a **genuine strategy verdict**, not a bug.

## 1. The audit question

The marker (`last_bar_time`) advancing proves *bars arrived* — not that
each was *evaluated*. A crash-then-resume could skip evaluation silently;
the marker alone can't distinguish "evaluated, no signal" from
"never evaluated". Phase 10 adds the missing evidence layer.

## 2. Decision ledger — `shadow_decisions` (migration 000018)

One **append-only row per evaluated close**:

| Field | Evidence |
|---|---|
| `bar_time`, `decided_at` | which bar, when evaluated |
| `source` | `live` vs `recovery` — replay never conflated with live |
| `action`, `reason` | strategy's raw verdict ("no crossover", "warm-up", "crossed above") |
| `fast_ema`, `slow_ema` | indicator values *at evaluation time* — not recomputed later |
| `actionable`, `blocked_reason` | whether an order was gated, and why (`already_long`, `flat_no_position`, `pending_order_active`, `missed`, `unobservable`) |
| `order_id`, `session_id` | links decision → order → session |

UNIQUE on (strategy, version, symbol, tf, bar_time) — first write wins:
`ON CONFLICT DO NOTHING` keeps the original evaluation immutable (a
recovery replay can never overwrite `decided_at`, indicator values, or
actionability). `decide()` is the single chokepoint: every close path —
live and recovery — records the ledger row *then* persists the marker,
so a bar can never advance without a decision record.

**Phase-11 correction:** the earlier `DO UPDATE SET decided_at=…` clause
clobbered the original evaluation timestamp on any re-evaluation —
replaced with `DO NOTHING` (proven by `TestDecisionLedgerFirstWriteWins`):
the live evidence survives replay intact.

## 3. Zero-order state — explained by evidence, not assumption

Live verified at the 19:00Z close (first ledger row):

```
bar_time=19:00Z label · source=live · action=neutral · reason="no crossover"
fast_ema=4013.07 · slow_ema=4028.29 · actionable=false · order_id=NULL
```

**Independent recomputation** on the same stored closes: EMA20 = 4014.6,
EMA50 = 4027.5 — fast **below** slow by −0.32% for the whole window. For a
BUY cross fast must cross *above*; the gap has persisted across every
evaluated bar. The engine stays flat because the strategy says so —
**0 orders is the correct outcome of the frozen config**, not a
malfunction.

## 4. Session-8 state (post-deploy, restartCount=3)

```
config fingerprint   fillTimeoutMs=600000 · quoteMaxAgeMs=30000 ·
                     equity=100000 · size=10 · comm=0.02  (effective values)
orders / fills / P&L 0 / 0 / 0 — honest
decisions            1 recorded (bar 19:00Z, neutral) — ledger live
coverage             row 13 heartbeating, feed_state=connected, 0 drops
feed_events          recovered×2 (startup connects), none in-window
position             flat; equity 100000
```

## 5. Acceptance criterion met

"Genuine absence of crossover" vs "evaluation failure" are now
distinguishable by inspection: a bar with no ledger row = not evaluated;
a row with `action=neutral` = evaluated, no signal. Every order is
reconstructible: decision → candle → EMA values → (order → fill → P&L).

## 6. Changes

Migration 000018 (`shadow_decisions`), `ShadowDecisionRow`,
`RecordDecision`/`ListDecisions` (PG + interface + memStore), engine
`recordDecision`/`decide` on every close path, `Metrics.Decisions`,
`GET /api/shadow/decisions`, frontend `Recent evaluations` strip +
`useShadowDecisions`, `TestDecisionLedgerRecordsEveryEvaluation`.

Checks: `go test ./...`/`vet`/`build` clean (15/15 shadow); `tsc`/
`eslint` clean; vitest 58/58; `vite build` ✓. **Uncommitted, no secrets,
no orders.**
