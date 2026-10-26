# Shadow Execution Reliability & Forward Validation — Phase 5

**Date:** 2026-10-26 · **Companion to:** `shadow-trading-2026-10.md`

The critical item this phase: downtime semantics. Phase 4's restart path
processed missed closes via the live decision path — a signal that became
actionable while offline could fill at the first post-restart quote, long
after its window. That is now a defined, evidence-bound recovery policy.

## 1. Lifecycle timestamps (exact meanings — not interchangeable)

| Field | Meaning |
|---|---|
| `signal_bar_time` | Canonical session label — the bar's open-aligned label (Kyiv sessions) |
| decision anchor | `signal_bar_time + timeframe duration` = the instant the close confirmed and the signal became actionable |
| `decision_at` | Live closes: when the engine acted (`now()`). Recovered closes: the **anchor**, not recovery time — honest earliest-execution instant |
| `quote_provider_time` | Provider event time |
| `quote_received_at` | Local receipt time (freshness = receipt − provider ≤ 30s) |
| `filled_at` | Live: fill computation time. Replay: the evidence observation's received_at |
| `created_at` | DB insert time — distinguishes "when we processed" from "when it became actionable" |

## 2. Recovery policy (`ProcessMissedClose`)

For each bar that closed while offline:
1. Evaluate the (warmed) strategy stream at that close — signals are
   real evaluations, not replayed decisions.
2. If actionable and the **window is still open** → pending resumes; only
   quotes with `provider_time ≥ anchor` qualify.
3. If the **window passed** → `FindQuoteInWindow` searches
   `quote_observations` for the earliest admissible quote in
   `[anchor, anchor+timeout)` (valid legs, uncrossed, provider_time present,
   not stale-flagged). Found → deterministic replay fill at the recorded
   quote (`filled_at` = evidence receipt). Not found → `status='missed'`
   with reason — evidence absence is recorded, never fabricated.
4. `order_key` + `last_bar_time` make the whole path idempotent.

**Forbidden outcomes, now structurally impossible:** fill for a passed
window from a post-restart quote; backdated fill; duplicated order from
replayed recovery; manufactured quotes.

## 3. Sessions (auditability)

`shadow_sessions` — one open row per `(strategy@version, symbol, tf,
provider)` holding the frozen config JSON (equity, sizing, commission,
fill timeout, quote freshness bound). Restart resumes the same session
(`restart_count++` — verified live: id=1, restarts=1 after a restart);
a config change starts a clearly distinguished new session. Orders and
positions carry `session_id`. Never silently resets state.

## 4. Operational metrics (real counters, API + UI)

`barsProcessed`, `signalsSeen`, `quotesSeen`, `quotesRejected`, `fills`,
`expired`, `missed`, `recoveries`, `duplicateEventsSkipped`,
`lastQuoteAt`, `lastFillAt` — plus order stats including `missed`, and
the composite state badge on `/shadow` (provider state × quote freshness
× pending/position → "watching / awaiting quote / stale / disconnected").
Absence of trades is shown as "no eligible signal", not a failure.

## 5. Evidence

**Deterministic tests (12 engine + recovery + integration):**
- `TestRecoveryMissedNoEvidence` — no quote evidence → `missed`, no fill
- `TestRecoveryReplaysPersistedEvidence` — replay at recorded ask;
  `decision_at` = anchor, `filled_at` = evidence receipt
- `TestRecoveryIsIdempotent` — re-run recovery → still 1 order
- `TestRecoveryWindowStillOpen` — pending resumes; pre-anchor quote refused
- `TestRestartBeforeSignal` — marker survives, post-restart close decides once
- `TestShadowIntegrationWithPostgres` — REAL engine + REAL postgres:
  signal → 1 order → ask-fill → exit-signal → bid-fill → P&L reconciles
  → restart+replay → still exactly 2 orders

**Live verification:** session 1 resumed across restart (restarts=1);
313–478 quotes processed with 0 bogus fills; 1,156 quote observations in
10 min all uniquely keyed. Zero orders — no EMA cross occurred on real
closes during observation; that outcome is honest, not a defect.

## 6. Remaining limitations for a longer paper-trading run

1. Indicative feed ≠ broker-executable fills (spread is ~0.25bp Tiingo top-of-book).
2. Quote gaps: `missed` records evidence absence — can't distinguish "no
   market quote existed" from "recorder was also down". Recorder is in the
   same process; a combined outage produces missed-not-filled — honest.
3. `decision_at=now` for live closes vs anchor for recovery — the UI
   should show `signal_bar_time` for causality; both fields are persisted.
4. Recovery depth is bounded by the warmup window (400 bars).
5. 1h EMA20/50 crosses infrequently — a meaningful forward session needs
   weeks, not hours; the protocol (Phase 3 §6) still governs promotion.

**Files**: migration 000014 (sessions + `missed`), `postgres/shadow.go`
(sessions, `FindQuoteInWindow`), `postgres/quote_obs.go` (evidence lookup),
`shadow/engine.go` (`ProcessMissedClose`, `Metrics`, session stamping,
shared `fillOrder`), `service/shadowdriver.go` (recovery + session
bootstrap), `service/shadow.go` + contract (session/metrics/status),
`pages/Shadow.tsx` (session/metrics/op-state/missed), engine tests
(5 recovery + 1 integration), `integration_test.go`.

Checks: `go test ./...`/`vet`/`build` clean (13 shadow tests incl.
real-postgres integration, gated on `GOLDMINE_TEST_DSN`); `tsc`/`eslint`
clean; vitest 58/58; `vite build` ✓. **Uncommitted, no secrets, no orders.**
