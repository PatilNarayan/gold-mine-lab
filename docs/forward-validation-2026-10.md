# Forward Shadow Trial — Coverage, Recovery & Frozen Protocol — Phase 6

**Date:** 2026-10-26 · **Companion to:** `shadow-reliability-2026-10.md`

The two observability gaps from Phase 5 are closed: execution windows now
carry **observed vs unobservable** classification, and recovery is bounded
by the persisted marker instead of a 400-bar cap.

## 1. Quote-evidence coverage (item 1)

`quote_coverage` (migration 000015): one row per recorder run —
`started_at`, `last_heartbeat_at` (30s cadence, independent of quote
arrivals), `last_obs_at`, `obs_count`, `dropped_batches`, `ended_at`.

- A heartbeat proves *observation capability* — "alive and persisting" is
  distinct from "quotes existed" and from "WebSocket connected".
- Clean shutdown writes `ended_at`; a crash leaves it NULL and the
  heartbeat goes stale — either way, windows past the last heartbeat are
  honestly **unknown**.
- `dropped_batches > 0` on the covering row → coverage incomplete
  (conservative: a failed batch anywhere on the row degrades it — see §7).

`CoverageComplete(from,to)` = a row exists with `started_at ≤ from`,
`last_heartbeat_at ≥ to`, `COALESCE(ended_at,heartbeat) ≥ to`, and
`dropped_batches = 0`.

### Execution-window classification

| Evidence state | Order status |
|---|---|
| Admissible quote in window | `filled` (replay at recorded quote) |
| Coverage complete + no admissible quote | `missed` — *evidence of absence* |
| Coverage incomplete / unknown | `unobservable` — outcome indeterminate |
| Live order times out | `expired` |
| Quote rejected by policy | `rejected` (reason recorded) |

An outage can never masquerade as "no quote existed" again.

## 2. Recovery beyond the warmup cap (item 2)

Old: `Tail(400)` — any downtime older than 400 closes was invisible.
New: `Range(lastBar, now, warmup=200)` on the persisted marker —
indicator warmup (200 bars strictly before the marker) + **every** stored
close after it. Bound = the marker, not a cap. A 3-week outage recovers
the full gap chronologically; every eligible decision resolves via the
Phase-5 evidence rules. Fresh starts still warm only (no retro-decisions).

## 3. Frozen forward-trial benchmark (item 4)

Session **#1** (open, `restartCount=2`, started 2026-10-26 22:52+0530):

| Parameter | Value |
|---|---|
| Strategy | `ema-cross` v1.0.0 (BENCHMARK/CONTROL — no demonstrated edge) |
| Config | fast=20, slow=50, warmup=150 |
| Symbol / TF / Provider | `XAU/USD` · `1h` · `tiingo` |
| Equity / sizing | 100,000 virtual · 10% per position |
| Cost assumption | commission 0.02%/fill (assumption, not measured) |
| Fill policy | entry=ask, exit=bid; uncrossed; provider_time ≥ decision; staleness ≤30s receipt-lag; FillTimeout=10min |
| Signal timing | confirmed-closed candles only |
| Fingerprint | session row holds config JSON + provider; order_keys carry version |

Changing any of these = new strategy version + new session — never a
silent reset of session #1.

## 4. UI operational state (`/shadow`)

Session identity + restart count · recorder coverage id + last persisted
observation + dropped batches · provider state badge (connected / stale /
disconnected, never "connected ⇒ healthy") · pending / filled / rejected /
expired / **missed** / **unobservable** order counts · open position ·
realized P&L + assumed commissions + measured spread cost · engine
counters (bars, signals, quotes, recoveries, duplicate-skips).

## 5. What the forward trial collects

Decision anchors and decision timestamps · provider vs receipt times ·
entry/exit bid+ask+mid · spread (price + bp) at each fill ·
decision-to-fill delay · missed vs unobservable windows · coverage gaps +
recorder outages (`dropped_batches`, heartbeat staleness) · position
lifecycle + net hypothetical P&L. All indicative-feed observations —
never retail-executable claims.

## 6. Test evidence (all deterministic except the gated PG test)

| Test | Proves |
|---|---|
| `TestRecoveryMissedNoEvidence` | covered window + no quote → `missed` |
| `TestRecoveryUnobservableWhenCoverageUnknown` | unknown coverage → `unobservable` |
| `TestRecoveryReplaysPersistedEvidence` | replay at recorded ask; anchor decision_at |
| `TestRecoveryBeyondWarmupDepth` | 700 closes; evidence-fill + unobservable at depth |
| `TestRecoveryIsIdempotent` | re-run recovery → still exactly 1 order |
| `TestRecoveryWindowStillOpen` | open window → pending; pre-anchor quote refused |
| `TestRestartBeforeSignal` | marker survives restart; live close decides once |
| `TestShadowIntegrationWithPostgres` | full lifecycle through real PG (env-gated) |
| Phase-4/5 suite | ask-entry, bid-exit, quality, expiry, idempotence, session resume |

Live verification: coverage row id=1 heartbeating (97 obs, 0 drops);
session #1 resumed across 2 restarts; 0 orders — no eligible EMA cross
occurred; that is honest signal absence, not a system gap.

## 7. Remaining limitations for a longer trial

1. **Coverage granularity**: `dropped_batches` is row-lifetime — one early
   failure degrades that interval's later windows to `unobservable`
   (conservative; per-window tracking would be heavier).
2. **Recorder shares the backend process**: a backend outage = no
   coverage AND no recording — windows classify `unobservable` (honest),
   and recovery can never prove what was missed.
3. **Indicative feed**: ~0.25bp Tiingo top-of-book ≠ retail executable.
4. **Heartbeat clock = DB server `now()`**: machine clock skew between
   app and DB would shift coverage bounds (bounded by heartbeat cadence).
5. **Forward evidence remains thin**: 1h crosses are rare; meaningful
   trial = weeks. The Phase-3 protocol (≥60 decisions or 6 months) still
   governs any promotion.

**Files**: migration 000015, `postgres/quote_obs.go` (coverage CRUD +
`CoverageComplete`), `service/quoterecord.go` (coverage lifecycle),
`shadow/engine.go` (`unobservable`, `Unobservable` metric),
`service/shadowdriver.go` (Range-bounded recovery), `service/shadow.go` +
contract (recorder/unobservable/session), `pages/Shadow.tsx` +
domain schemas + mock, `docs/forward-validation-2026-10.md`.

Checks: `go test ./...`/`vet`/`build` clean (14 shadow tests +
PG integration); `tsc`/`eslint` clean; vitest 58/58; `vite build` ✓.
**Uncommitted, no secrets, no real orders.**
