# Forward-Trial Readiness — Phase 12

**Date:** 2026-10-27 · **Verdict: READY to accumulate prospective evidence.**

## 1. Decision→order linkage — verified immutable & complete

- `RecordDecision`: `ON CONFLICT DO NOTHING` — first-write-wins; the
  original `decided_at`/indicator values/actionability can never be
  overwritten by a replay (proven: `TestDecisionLedgerFirstWriteWins`).
- **New constraint** — `CHECK (actionable = false OR order_id IS NOT NULL)`:
  an actionable decision must link its order; a decision can never claim
  actionability while unlinked.
- Ordering: `decide()` records the ledger row **before** the marker —
  a bar can never advance without evidence; a failed ledger write blocks
  marker persistence → recovery re-evaluates → replay re-links the order
  (`CreateOrderIfAbsent` dedupe + recovery path always passes `&id`).
- No dangling state is reachable: order exists ∧ decision missing ⇒
  recovery re-eval reconstructs the link; decision exists ∧ order
  missing ⇒ only for non-actionable rows (correct).

## 2. Trial state (verified live)

```
session        8 — running ~1h since deploy, restartCount=4
decisions      1 recorded (19:00Z close): neutral / no crossover
               fast_ema=4013.07 vs slow_ema=4028.29 — still below; no BUY
orders/fills   0 / 0 — honest absence, not a defect
coverage       heartbeating, feed_state=connected, 0 drops
metrics        decisionsEvaluated surfaced via API
```

## 3. What the evidence chain now proves

Per evaluated candle (ledger row): bar label, evaluation moment, source
(live|recovery), EMA values at eval time, verdict + reason, gate outcome
(blocked_reason), order link when produced. Per order: creation,
execution-window evidence or coverage verdict, fill at quote-admissible
bid/ask, P&L, tx-atomicity. Per session: identity, frozen fingerprint,
restarts. Per coverage: recorder aliveness + feed transitions + drop
accounting.

"Evaluated, no signal" vs "never evaluated" vs "signal, no order" are all
distinguishable by inspection.

## 4. Readiness criteria (unchanged)

Promotion review when: ≥60 eligible decisions **or** ≥6 months, with
execution-integrity metrics reported independently of P&L. Current: 1
decision — accumulation started.

## 5. Files changed (Phase 11+12)

`postgres/shadow.go` (DO NOTHING), `engine_test.go` (+2 ledger tests),
migration 000018 (+ linkage CHECK), `docs/session-8-baseline-2026-10.md`,
`docs/shadow-audit-2026-10.md`, `docs/forward-readiness-2026-10.md`.

Checks: `go test`/`vet`/`build` clean; `tsc`/`eslint`; vitest 58/58;
`vite build` ✓. **Uncommitted, no secrets, no orders.**
