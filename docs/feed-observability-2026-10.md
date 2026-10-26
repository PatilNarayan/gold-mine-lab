# Feed-Observability Hardening & Prospective Session — Phase 8

**Date:** 2026-10-26 · **Companion to:** `coverage-audit-2026-10.md`

## 1. The residual gap and how it's closed

Phase 7's coverage model sampled feed health at every heartbeat — a
disconnect between samples escaped detection. The providers already know
transitions immediately (`setState` is the single chokepoint for
connected→disconnected→reconnecting, including the watchdog's
"no frames within heartbeat window" dead-connection close).

**New mechanism (migration 000017 + provider instrumentation):**

- `SubscribeState(fn)` added to Tiingo and TwelveData — optional,
  type-asserted hook; the `live.Provider` interface is unchanged.
- `feed_events` table: `disconnect`/`degraded` (interval-opening),
  `recovered` (interval-closing), `persist_fail` (point) — each persisted
  **at event time** with `at` (detection) and `since` (the honest bound:
  last proven-healthy evidence, i.e., last persisted quote receipt or the
  provider's newest quote time). Exact disconnect instants are never
  inferred later than detection.
- `CoverageComplete` now additionally requires **no degraded interval
  overlapping the window** — `COALESCE(since,at) < to` AND
  `next-recovery-or-∞ > from`. Point containment alone missed outages
  that *span* a window; persist_fail is point-scoped (in-window only).

**What can now be claimed:** a disconnect → the wsSupervise detects the
dead read immediately → `setState` → recorder writes a `disconnect` event
synchronously → any overlapping execution window is `unobservable`.
Sub-second flaps between watchdog ticks: the provider's own frame-timeout
watchdog bounds detection; quotes arriving through the window still prove
admissibility regardless.

**What is NOT claimed:** an interval between the watchdog's detection
window and the event's `since` bound can only be "at worst started at
`since`" — conservatively treated as degraded from `since`. A disconnect
outside Tiingo's control (network path) is bounded by the watchdog, not
perfect.

## 2. Coverage predicate — final contract

Window [from,to) is **completely observable** iff ALL hold:

1. coverage row `started_at ≤ from` and `last_heartbeat_at ≥ to`
   (recorder alive + persisting through it);
2. `last_heartbeat_gap_at < from` or absent (no self-detected stall);
3. `last_drop_at < from` or absent (no persistence failure in window);
4. `last_feed_degraded_at < from` or absent (no sampled degradation);
5. no `feed_events` degraded-interval overlapping [from,to)
   (no transition-time disconnect/degraded spanning or inside it).

`missed` = all proven + no admissible quote. `unobservable` = anything
unproven. `filled` (replay) = admissible persisted quote in-window.

## 3. Interruption test evidence (real PG, env-gated)

`TestCoverageCompleteScenarios` — **12/12 pass**: healthy span · no row →
unknown · heartbeat died in-window · sampled degradation in/out-of-window ·
batch drop in/out-of-window · heartbeat gap in-window · **disconnect
spanning window (detected via `since` bound)** · recovery before window ·
disconnect inside window · in-window outage · persist_fail in/out.

`TestQuoteRecorderFeedEvents` — transition→event mapping: disconnect and
recovered events persisted under the active coverage id; computed `stale`
produces no spurious event.

`TestShadowIntegrationWithPostgres` — full lifecycle through real PG
still passes (signal→order→ask-fill→bid-exit→P&L→restart→no-dup).

## 4. Sessions: #1 preserved, #8 prospective

- **Session #1** (started 17:22Z, 6 restarts, 0 orders — no eligible
  cross): `ended_at` set, all records preserved as auditable history.
- **Session #8** — new prospective session, frozen identical config:
  `ema-cross` v1.0.0 · fast=20/slow=50/warmup=150 · XAU/USD·1h·tiingo ·
  equity 100k · size 10% · comm 0.02% · ask-entry/bid-exit · staleness
  ≤30s · FillTimeout=10min · confirmed closes only. restartCount=0,
  fresh order/P&L state; marker continuity preserved (engine stream
  position is shared identity, not session state). IDs 2–7 are
  ON-CONFLICT sequence gaps — no lost sessions.

## 5. Live evidence

- Session #8 observing: coverage #12 heartbeating, `feed_state=connected`,
  381+ obs/session, `droppedBatches=0`.
- `feed_events` already carrying real transitions: 2×`recovered`
  (startup connect transitions) + 1×`persist_fail` (a real persistence
  blip during restart churn — the mechanism caught it honestly).
- 0 orders — no eligible EMA cross on real closes; honest absence.

## 6. Remaining limitations

1. Detection bound = provider watchdog (frame timeout), not instant —
   bounded, honest, never claimed otherwise.
2. `since` can only bound the outage start, not pinpoint it — degraded
   interval is conservative (from last healthy evidence).
3. Indicative feed ≠ executable; ~0.25bp top-of-book is not retail cost.
4. Recorder shares the backend process — a crash = unknown coverage.
5. Weekend/quiet-market staleness → degraded → `unobservable` — correct
   (silence and outage are indistinguishable at the bound).

**Files:** migration 000017, `live/tiingo.go` + `twelvedata.go`
(`SubscribeState` + transition firing), `repository.go` (`FeedEventRow`),
`postgres/quote_obs.go` (`InsertFeedEvent`, overlap predicate),
`service/quoterecord.go` (transition→event + persist_fail events),
tests (`TestCoverageCompleteScenarios` 12 cases, `TestQuoteRecorderFeedEvents`).

Checks: `go test ./...`/`vet`/`build` clean; `tsc`/`eslint` clean;
vitest 58/58. **Uncommitted, no secrets, no orders.**
