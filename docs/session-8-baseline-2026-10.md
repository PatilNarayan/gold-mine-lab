# Shadow Session #8 — Operational Audit & Forward-Trial Baseline — Phase 9

**Date:** 2026-10-26 · **Read-only audit** → 3 defects found, all fixed (see §4)

## 1. Active session — verified against DB + live API

| Field | Value |
|---|---|
| Session | **#8** (open, `restart_count=2`, started 2026-10-26 18:12:44Z) |
| Strategy | `ema-cross` v1.0.0 · fast=20/slow=50/warmup=150 |
| Symbol / TF / Provider | `XAU/USD` · `1h` · `tiingo` |
| Frozen config (post-repair fingerprint) | equity 100k · size 10% · comm 0.02% · FillTimeout=600000ms · QuoteMaxAge=30000ms |
| Position / pending | **flat · 0 pending · 0 orders total** |
| Marker | `2026-10-26 19:00Z` (newest confirmed close) |
| Session-scoped obs | **1,002** observations since 18:12:44Z (cumulative DB total: ~29.8k) |
| feed_events in session | 2× `recovered` (startup connect transitions — health onset) |
| Retired session | **#1 preserved** (`ended_at=18:12:35Z`, all records intact, restartCount=6) |
| Inheritance | None — session #8 has zero orders/P&L; engine marker continuity preserved (stream state, not session state) |

Zero orders is **honest**: no eligible EMA20/50 cross has occurred on
confirmed closes since session start — not a defect.

## 2. Coverage audit — verified

`CoverageComplete` conditions all hold for the session window: coverage
rows (#12, #13) heartbeating with `feed_state=connected`,
`dropped_batches=0`, no degraded intervals, no heartbeat gaps.

**feed_events** — all real transitions persist (verified live earlier:
`recovered` ×2 startup connects; `persist_fail` ×1 in a pre-trial run).
Overlap semantics verified by the 12-case PG test (spanning disconnect,
in-window outage, persist_fail in/out — all classified correctly).

**Uncertain interval — flagged, not repaired:** coverage row **#11**
(15:12→17:13Z, heartbeat died without `ended_at`): `obs_count=0` while
1,298 obs rows landed in its real-time span, and `last_feed_degraded_at`
set at its start. Reconstruction suggests an orphan recorder (restart
churn) — its own feed dead the whole run while another (coverage-less)
recorder persisted obs. **Cannot be proven either way → classified
uncertain.** It entirely predates session #8's start, so the prospective
trial window itself is unaffected. Recorded here per policy; no records
altered or deleted.

## 3. Forward-trial baseline (reproducible snapshot)

```
session           8 (open, restarts=2, started 18:12:44Z)
config            {"commissionPct":0.02,"fillTimeoutMs":600000,
                   "initialEquity":100000,"quoteMaxAgeMs":30000,
                   "positionSizePct":10}
provider          tiingo / XAU/USD / 1h / websocket
coverage          rows 12,13 heartbeating; feed_state=connected
observations      1,002 (session-scoped, all uniquely event-keyed)
feed events       2 recovered (startup); 0 disconnect/degraded in-session
decisions/orders  0 signals / 0 orders / 0 pending
position          flat; realized 0; equity 100000
marker            19:00Z confirmed-close
known gaps        none inside session window; pre-trial interval flagged
```

## 4. Defects found & fixed (minimal, honest)

| # | Defect | Fix |
|---|---|---|
| 1 | `sessionConfig()` serialized the **pre-default** cfg → session fingerprints stored `fillTimeoutMs:0`/`quoteMaxAgeMs:0` while the engine ran 10min/30s | `EffectiveConfig()` exposed; driver serializes the engine's actual config. Session #8's stored JSON repaired once (runtime policy never changed); future mismatches trigger **new-session** separation — `GetOrCreateSession` now compares the fingerprint on resume |
| 2 | `dropped_batches` column dead since the Phase-7 heartbeat rewrite — `persist_fail` events existed the column never reflected | `CoverageSample.DroppedBatches` restored to the heartbeat UPDATE |
| 3 | `last_active_at` never touched after creation — `TouchSession` dead code | Called on every processed bar close |

No historical orders/fills existed to reclassify; no records deleted.

## 5. When is enough evidence?

Per the Phase-3 protocol, promotion criteria for a candidate (unchanged):
≥**60 eligible decisions** or ≥**6 months** of continuous session,
with execution-integrity metrics (decision→fill delay, spread,
missed/unobservable rates) reported independently of P&L. Session #8 is
at ~0 decisions — the trial is *running*, not complete.

## 6. Files changed

`internal/shadow/engine.go` (`EffectiveConfig`),
`internal/service/shadowdriver.go` (config serialization + TouchSession),
`internal/repository/repository.go` (`CoverageSample.DroppedBatches`),
`internal/repository/postgres/quote_obs.go` (heartbeat writes drops),
`internal/repository/postgres/shadow.go` (config-compare resume),
`docs/session-8-baseline-2026-10.md`.

Checks: `go test ./...`/`vet`/`build` clean; `tsc`/`eslint` clean;
vitest 58/58; `vite build` ✓. **Uncommitted, no secrets, no orders.**
