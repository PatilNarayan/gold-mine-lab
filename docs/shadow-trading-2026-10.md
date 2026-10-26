# Shadow Trading & Quote-Store Integrity — Phase 4

**Date:** 2026-10-23 · **Companion to:** `ema-cross-robustness-2026-10.md`

Restart-safe simulated execution against the real Tiingo quote stream.
EMA20/50 runs as the benchmark **control only** — it has no demonstrated
edge; this layer exists to validate signal→execution plumbing, not to
endorse a strategy. **No broker, no orders, no real money.**

## 1. Quote-store reconciliation (item 1)

| Figure | Actual |
|---|---|
| Reported ~4,362 obs / 291 post-fix | Snapshot mid-collection; final count **18,834** |
| Duplicate events | **0** — `(provider_time, bid, ask)` unique across all rows |
| Missing bid/ask | 0 rows (Tiingo supplies both legs on this feed) |
| NULL provider_time | 0 rows |
| Stale-flagged | 0 rows |
| Distinct `event_key` | 18,834 = total — **every row a verified distinct event** |

**Event identity** = `provider|symbol|provider_unix_ms|bid|ask|mid`
(canonical shortest-round-trip floats). Unprovable identity when
`provider_time IS NULL` → key NULL → stored but never deduplicated and
never counted as verified-unique. **Same-price successive events are NOT
duplicates** — provider timestamp distinguishes them (as required).

**Durable idempotency** (migration 000012): `event_key` column + partial
UNIQUE index `WHERE event_key IS NOT NULL`; insert uses
`ON CONFLICT DO NOTHING`. Dedup now survives restarts and concurrent
writers at the DB level, not just the in-memory last-key cache. Migration
was initially written as a GENERATED column — rejected at apply time
(`extract()` is STABLE not IMMUTABLE); rewritten as a plain column
computed in Go with a single canonical format, backfilled by UPDATE.

## 2. Shadow-execution contract

**Timing model:** signal evaluated **only on confirmed-closed candles**
(the live engine never sees a forming bar's close) → order decision →
fill at the **first qualifying quote after the decision**.

**Fill policy:** entry at observed **ASK**, exit at **BID**. Mid is
recorded for comparison, never executed. A quote qualifies only if:
- bid AND ask present, both > 0, ask ≥ bid (uncrossed),
- provider timestamp present and ≤ 30s behind receipt (fresh),
- provider timestamp ≥ decision time (a quote can't predate its cause).

Rejects: missing legs, crossed, stale, predated → order stays pending;
>10 min without a qualifying quote → `expired` with reason recorded.
Nothing is invented, nothing is silently filled at mid.

**Costs:** commission = configured % of notional per fill (0.02% — same
assumption as the benchmark backtest, recorded *as an assumption*).
Spread cost = measured |fill − mid| × qty, accumulated separately — real
observation vs assumption kept distinct. Spread is charged once: the ask
IS the fill, not mid-minus-spread on top.

## 3. Idempotency & restart safety

- **order_key** = `strategy@version|symbol|tf|bar_time|side` — UNIQUE.
  Replayed closes, WS reconnects, and multi-consumer delivery collapse to
  one row (`ON CONFLICT DO NOTHING`).
- **Marker** `shadow_positions.last_bar_time` — newest processed close;
  re-delivered bars at/before it are ignored before any evaluation.
- **Atomic transitions**: order-fill + position update commit in one
  transaction (`FillOrderAndPosition`) — a crash can't split them.
- **Restore**: on startup the engine reloads position + pending order +
  marker from the DB, re-warms the evaluator on 400 stored candles.
  - *Fresh start*: stored bars warm the stream and set the marker —
    the engine must NOT act on signals that closed before it existed.
  - *Restart*: bars that closed during downtime DO process (real missed
    decisions, `decision_at=now`, never backdated).
- **Pending orders** survive restart via `GetPendingOrder` resume.

## 4. Architecture

```
Tiingo WS ──► QuoteRecorder ──► quote_observations (event_key UNIQUE)
         └──► ShadowDriver ──┬─── quotes ──► Engine.OnQuote (fill policy)
              books poll ────┴─── closed bar ──► Engine.OnBarClosed
                               (15s tick, bars[n-2] = newest confirmed)
                               │
         strategy.Stream (EMA20/50 stream — SAME evaluator as research)
                               │
         shadow_orders (decisions/fills) + shadow_positions (state)
                               │
              GET /api/shadow/status | /orders → /shadow page
```

- `internal/shadow/engine.go` — pure deterministic event reducer.
- `internal/service/shadowdriver.go` — wiring: warmup, quote fan-out,
  close-detection ticker, expiry ticks.
- `internal/repository/postgres/shadow.go` — transactional persistence.
- `/shadow` page — SIMULATED banner, virtual equity, position, pending
  order, order table with fill-vs-bid/ask detail, honest empty states.

## 5. Test evidence (deterministic, in `internal/shadow/engine_test.go`)

| Test | Proves |
|---|---|
| `TestShadowEntryFillsAtAsk` | Entry at 100.10 ask not 100.05 mid; missing-ask quote refused; qty sizing; commission recorded |
| `TestShadowExitFillsAtBidReconciles` | Exit at bid; net = gross − both commissions to 1e-2 |
| `TestShadowDuplicateCloseIsIdempotent` | Replay×3 → exactly 1 order row |
| `TestShadowRestartRecovery` | Fresh engine + same store → position/entry restored; replayed bar produces no new order |
| `TestShadowQuoteQualityAndExpiry` | Stale, crossed, missing-leg, predecision quotes refused; timeout → `expired` |
| `TestShadowIgnoresNonActionableSignals` | SELL-while-flat, BUY-while-long → no orders |
| `TestShadowOrderKeyStableAcrossEngines` | Same decision key across engine instances → 1 row |

Plus `TestQuoteRecorderDedupeAndSpread`/`StalenessFlag` (Phase 3) and the
`TestDBProviderRangeWarmupBoundaryNoDup` regression.

## 6. Live evidence (real feed, uncommitted)

- Engine live since 2026-10-23 ~18:20Z; marker advances only on real
  closes (verified: 17:00Z close processed, next close pending at 18:30Z —
  correct: a bar is closed only when its successor starts).
- Quote recorder: 18,834 distinct keyed events; insert conflicts = 0.
- An early pre-fix build did execute one stale-signal fill (BUY @ ask
  3999.14, comm $2, spread $0.125) — caused by missing fresh-start guard;
  convention fixed (§3), dev rows cleared, behavior verified clean.

## 7. Known limitations before this is credible paper-trading evidence

1. **Indicative feed**: Tiingo ~0.25bp top-of-book is not retail-executable;
   shadow fills are honest *feed* executions, not broker-realistic fills.
2. **Quote-feed gaps** mark orders expired but can't prove a fill was
   impossible — absence of a qualifying quote is evidence, not certainty.
3. **No candle->quote causality check on clock skew**: provider_time is
   trusted as event time; a mis-set provider clock could order events
   wrong (mitigated by the 30s receipt-lag freshness bound).
4. **Single-position benchmark only**: engine models one long position —
   fine for the control strategy; broader use needs its own audit.
5. **Paper P&L is unvalidated by any real reference** until a prospective
   window with adequate signal count accumulates (minimum evidence per
   the Phase-3 protocol: ≥60 decisions or 6 months).

**Files**: migrations 000012/000013, `internal/shadow/*`,
`postgres/shadow.go` + `quote_obs.go` (event_key), `service/shadow*.go`,
`api` contract + routes + `Deps.Shadow`, `main.go` wiring,
`pages/Shadow.tsx` + `httpShadow.ts` + `mockShadow.ts` + `useShadow.ts`
+ nav/router. **Uncommitted. No secrets touched. No real orders exist.**

Checks: `go build`/`vet`/`test ./...` clean (7 new engine tests + 2
recorder + 1 db regression); `tsc --noEmit` clean; `eslint` clean;
vitest 58/58; `vite build` ✓ (Shadow chunk emitted).
