# Shadow Pipeline Correctness Hardening — Phase 14

**Date:** 2026-10-27 · **Baseline:** commit `6b6bdb9` (all prior phases
committed; tree clean before editing).

## Confirmed root causes

1. **Label/real-time mixing (3 sites).** `bar_time` is a Kyiv civil-time
   label rendered naive-UTC (~+2/3h ahead of real UTC). Three sites
   compared it to real time:
   - `shadowdriver.go` recovery `Range(lastBar, time.Now().UTC())` — the
     upper bound was ~2–3h *behind* the label space → recovery silently
     missed the freshest stored closes.
   - `DBProvider.Tail` bound `p.now().UTC()` — newest stored bars
     (labels ahead of real UTC) invisible to tail queries.
   - `ProcessMissedClose` — `anchor = c.Time + tf` in *label space*,
     then `now().Before(windowEnd)` and `FindQuoteInWindow` against
     real instants → the execution window sat ~1–3h in the **real
     future**: recovered orders couldn't expire on time and
     `quoteUsable` rejected every real quote (provider_time < anchor).
     Latent — no live order had ever exercised it.

   **Fix:** `live.LabelToInstant(label)` (Kyiv wall-clock → real instant,
   DST-aware, verified summer +3 / winter +2) and `live.NowLabel()`.
   Anchors/DecisionAt/windows/coverage checks are now real instants;
   `bar_time`/`signal_bar_time`/order_key stay label-space (identity).

2. **Lost-bar risk on persist failure.** `OnBarClosed`/`ProcessMissedClose`
   advanced `pos.LastBarTime` *before* evaluation; a `persistBar` failure
   left the in-memory marker ahead of the durable one → the same bar
   would be skipped in-process until restart.
   **Fix:** `decide()` sets the marker, persists, and **reverts on
   failure**; `MarkBarProcessed` same. The stream would still have
   double-advanced on retry → `evaluate()` caches the computed signal
   (`lastEval`) keyed by bar; a retried bar reuses its signal — the
   stream never double-advances. Tests:
   `TestRetryAfterTransientPersistFailure`,
   `TestDecisionWriteFailureBlocksMarker`.

3. **Session isolation.** `Restore()` ran *before* `GetOrCreateSession`
   and positions were keyed by strategy identity (PK) — a new-config
   session would inherit position/marker/P&L.
   **Fix:** driver resolves session *first*; `GetPosition`/
   `GetPendingOrder` are session-scoped; PK of `shadow_positions` is now
   `(session_id, strategy_id, version, symbol, timeframe)`; order_key
   includes `|s{session}`; decisions unique key is
   `(session_id, identity, bar_time)` `NULLS NOT DISTINCT`. Old session
   rows are untouched — auditable forever.

4. **Decision→order linkage on dedup.** A mid-flight failure could leave
   an order persisted with no decision row; the retry recorded
   `actionable=false`, `order_id=NULL` — the ledger would have lied.
   Now every path that reaches an existing-or-new order row records
   `actionable=true` + `order_id` — dedup is still an order *of this
   bar*, so it belongs to the decision.

## Files & migrations

`live.go` (`LabelToInstant`, `NowLabel`, docs) · `market/db.go`
(label-space Tail bound) · `shadowdriver.go` (session-first restore,
label bound) · `engine.go` (`evaluate`/`lastEval`, marker revert in
`decide`/`MarkBarProcessed`, real-instant anchor, dedup linkage,
SetSession stamping, session-scoped Restore) · `postgres/shadow.go`
(session-scoped Get/Upsert, NULLS NOT DISTINCT conflict) ·
`engine_test.go`/`integration_test.go`/`tiingo_test.go` ·
migration `000019_session_isolation` · README updates.

## Evidence

- `TestLabelInstantRoundTripDST`: label→instant correct in EEST(+3) and
  EET(+2); `NowLabel` offset inside Kyiv bounds.
- `TestRetryAfterTransientPersistFailure`: injected marker-write failure
  → same-process retry produces exactly the real order + linked decision;
  stream not double-advanced.
- `TestDecisionWriteFailureBlocksMarker`: failed ledger write → marker
  never advanced; retry persists exactly one row.
- `TestShadowIntegrationWithPostgres` (real PG): signal→order→ask
  fill→bid exit→P&L reconcile→replay no-dup — under session-scoped
  persistence.
- `TestLabelInstantRoundTripDST`: summer/winter label conversion.

## Trial state (before → after deploy)

session #8 · restarts 4→5 (deploy) · marker 20:00Z preserved · flat ·
0 orders/P&L · config fingerprint unchanged · obs still accruing
(283 this run).

## Remaining limits

- The recovery `Range` bound now reaches the true freshest labels —
  deeper recovery coverage depends on synced history existing for that
  span (it does for the 1h book).
- `session_id` NULL decisions (legacy/sessionless) dedupe under
  `NULLS NOT DISTINCT`; only relevant to non-session engines.
- Fall-back DST: two real hours share one label — resolution is
  deterministic (documented); evidence ambiguity is inherent to the
  broker convention, not a new artifact.

Checks: `go test ./...`/`vet`/`build` clean (18/18 shadow + real-PG
integration); DST round-trip verified; `tsc`/vitest 58/58 clean.
**Uncommitted, no secrets, no orders.**
