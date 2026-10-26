# Quote-Coverage Audit & Corrected Observability Predicate — Phase 7

**Date:** 2026-10-26 · **Companion to:** `forward-validation-2026-10.md`

## 1. Audit verdict: the old predicate was insufficient

`CoverageComplete` (Phase 6) required only a coverage row spanning the
window with `dropped_batches=0`. That proved *recorder aliveness + write
path* — it did **not** prove:

- **upstream feed health** — a running recorder over a dead/reconnected
  WebSocket heartbeats fine while seeing nothing;
- **heartbeat continuity** — `started_at ≤ from` + `heartbeat ≥ to` still
  allowed an unobserved stall *inside* the window;
- **window-scoped integrity** — `dropped_batches` was row-lifetime, so one
  early failure permanently degraded every later window in the row
  (over-conservative), while degradation *inside* a window was
  indistinguishable from one outside it (under-precise).

**Corrected rule — a window [from,to) is completely observable iff:**

| Requirement | Mechanism |
|---|---|
| recorder interval started before `from` | `started_at ≤ from` |
| recorder alive AND persisting past `to` | `last_heartbeat_at ≥ to` |
| no process stall inside the window | `last_heartbeat_gap_at < from` (self-detected at >2×cadence) |
| no failed persistence inside the window | `last_drop_at < from` |
| feed healthy inside the window | `last_feed_degraded_at < from` — heartbeat samples the managed provider's OWN `Status()` (state==connected ∧ newest-quote-fresh) |

Degradations wholly before a window no longer taint it — evidence is
**window-scoped** (`last_*` timestamps), not row-lifetime.

**No invented guarantees:** Tiingo provides no message-sequence or
heartbeat contract — the model uses only what the platform itself observes
(provider state transitions, quote freshness, write success). Sub-staleness
feed gaps that reconnect without evidence are a documented residual limit.

## 2. Missed vs unobservable — final semantics

| Evidence state | Status |
|---|---|
| admissible persisted quote in window | `filled` (replay at recorded quote) |
| **all five conditions proven** + no quote | `missed` — evidence of absence |
| any condition unproven | `unobservable` — outcome indeterminate |

## 3. Test evidence

`TestCoverageCompleteScenarios` (real PG, env-gated) — all 7 cases:
healthy span ✓ · heartbeat died in-window ✓ · feed degraded in-window ✓ ·
degradation *before* window stays complete ✓ · batch drop in-window ✓ ·
heartbeat gap in-window ✓ · no row → unknown ✓.

`TestShadowIntegrationWithPostgres` (lifecycle through real persistence)
still passes. All 14 shadow unit tests + recorder tests pass.

Live: coverage interval #6 heartbeating with `feed_state='connected'`,
degradation fields NULL (healthy), 381 obs persisted; session #1 resumed
across **4 restarts** (restartCount=4).

## 4. Frozen forward-trial benchmark — unchanged

Session #1 · `ema-cross` v1.0.0 · fast=20/slow=50/warmup=150 · XAU/USD·1h·
tiingo · equity 100k · size 10% · comm 0.02% · ask-entry/bid-exit ·
staleness ≤30s · FillTimeout=10min · confirmed-closes only. **No change.**

## 5. Files changed

migration 000016 (`last_feed_degraded_at`, `last_heartbeat_gap_at`,
`last_drop_at`, `feed_state`) · `postgres/quote_obs.go` (evidence
heartbeat, window-scoped predicate, expanded scan) · `repository.go`
(`CoverageSample`, row fields) · `service/quoterecord.go` (feed sampling,
stall self-detection, drop timestamps) · `api/contract.go` + `service/shadow.go`
(recorder evidence fields) · frontend schema + `/shadow` page · tests.

Checks: `go test ./...`/`vet`/`build` clean; `tsc`/`eslint` clean;
vitest 58/58; `vite build` ✓. **Uncommitted, no secrets, no orders.**
