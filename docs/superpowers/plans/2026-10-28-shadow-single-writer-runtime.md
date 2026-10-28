# Shadow Single-Writer Runtime — Implementation Plan

> Execution method: inline (user supplied spec + explicit "implement incrementally").

**Goal:** one mutation owner per shadow runtime; atomic durable bar transitions;
safe API snapshots; explicit lifecycle; ownership via Postgres advisory lock.

**Architecture:** a bounded-mailbox `shadow.Runtime` owns the `shadow.Engine`.
Producers (provider quote callback, driver bar/tick poll) submit events;
one owner goroutine dispatches them to the engine and republishes an immutable
snapshot after every event. Persistence failure ⇒ `unhealthy`, fail-stop
(restart required — the documented minimal-safe recovery). Bar transitions
(order + decision + position/marker) commit in ONE transaction.

**Spec:** user prompt sections 1–12 (in repo history).

## Verified baseline defects

- Engine mutated from 3+ goroutines (provider delivery, driver loop, HTTP).
- `fillOrder` advances `e.equity` before the fill tx commits.
- decision/order/marker are 3 separate writes per bar.
- driver ctx is `context.Background()` — never cancelled; pool closes first.
- `Snapshot().Pending` aliases mutable engine state; SessionID never set.
- No advisory lock → second instance can co-own a session.
- Warmup marker failures are logged-and-continued (stream/marker divergence).

## Tasks

### Task 1: Atomic bar transition (engine + store)

**Files:** `internal/shadow/engine.go`, `internal/repository/postgres/shadow.go`,
`internal/shadow/engine_test.go` (memStore), `internal/repository/postgres/commit?`

- Store iface += `CommitBar(ctx, BarCommit) (BarResult, error)`:
  tx = insert order-if-absent → optional fill-apply or terminal-status on that
  id → insert decision (dedupe) → upsert position/marker. `BarCommit{NewOrder,
  Resolve, Fill, Decision, Position}`; `BarResult{OrderID, OrderCreated}`.
- Engine: `OnBarClosed`, `ProcessMissedClose` emit one `CommitBar` per bar.
  Recovery evidence reads (FindQuoteInWindow/CoverageComplete) stay pre-tx.
- Fix `fillOrder`: apply `e.equity += net` only after commit succeeds.
- Add `Warmup(ctx, c)`: stream.Next + marker persist; failure is fatal to caller.
- `Snapshot()`: deep-copy `e.pending`; set `SessionID`.
- Tests first (RED): fail-store test asserting marker/equity don't advance;
  memStore gains CommitBar; existing 744-line suite must stay green.

### Task 2: Runtime (mailbox, health, snapshots, stop/drain)

**File:** `internal/shadow/runtime.go` (+ `runtime_test.go`)

- `Event{Kind: EvQuote|EvBarClosed|EvTick, Quote, Candle, Result chan error}`.
- `Runtime{eng, mailbox(sz=1024), snap atomic.Value, health, stats, stopOnce}`.
- `Start(ctx, bootstrap func(ctx, *Engine) error)` — owner goroutine runs
  bootstrap (session/restore/warmup/recovery) BEFORE consuming mailbox.
- `Submit(ctx, ev) error`: health check → `ErrUnhealthy`; non-blocking send →
  `ErrMailboxFull` AND degrade runtime to unhealthy (a dropped event may be the
  first admissible fill — never silently drop and claim coverage). Honors ctx.
- `Stop(ctx)`: idempotent; closes admission, drains mailbox while healthy or
  until ctx deadline; reports undrained count in stats; joins owner.
- `Snapshot() RuntimeSnapshot{Engine, Metrics, Health, Stats}` — published
  after every processed event; deep-copied.
- `Ownership` iface `{Lost() <-chan struct{}; Release(ctx) error}`; loss ⇒
  unhealthy; released on owner exit. `OwnershipKey(cfg)` FNV-1a int64.

### Task 3: Postgres ownership + CommitBar

**Files:** `internal/repository/postgres/ownership.go` (new), `shadow.go`

- `AcquireShadowOwnership(ctx, key int64) (*ShadowOwnership, error)`:
  dedicated `pool.Acquire` conn → `pg_try_advisory_lock` → watchdog `conn.Ping`
  every 3s → `lost` on conn death; `Release` unlocks + releases conn.
- `CommitBar` tx impl reusing existing SQL shapes.

### Task 4: Driver + service + API + frontend schema

**Files:** `internal/service/shadowdriver.go`, `internal/service/shadow.go`,
`internal/api/contract.go`, `frontend/src/types/domain.ts`

- Driver = producer wiring only: `Bootstrap` closure (session → restore →
  warmup/recovery via eng directly, on owner goroutine); quote sub →
  `rt.Submit(EvQuote)`; ticker → EvTick + poll `provider.Candles` → EvBarClosed.
  Stops producing when `rt` unhealthy; ctx.Done exits.
- Service reads `rt.Snapshot()` (no engine access); `ShadowStatusResponse` +=
  additive `runtime {state, reason, eventsSubmitted/Processed/Rejected/Dropped}`.
- Frontend: add optional `runtime` zod object (additive, non-breaking).

### Task 5: main.go lifecycle + wiring

**File:** `cmd/server/main.go`

- `runCtx, cancel := context.WithCancel`; acquire ownership → rt + driver.
- Shutdown: `srv.Shutdown` → `cancel()` (detach producers) → `<-driverDone` →
  `rt.Stop(shutdownCtx)` → `quoteRec.Stop()` → `provider.Close()` →
  `pool.Close()` LAST. Undrained work reported in logs.

### Task 6: Tests + docs

- `runtime_test.go`: concurrency, dup bars, persistence-failure (checkpoint not
  advanced, no partial memory, ambiguous-commit inspected on restart), recovery
  instants, overflow policy, lifecycle (stop-under-producers, drain, deadline,
  second-owner-refused via fake), determinism (same inputs+clock ⇒ same
  committed outcome).
- Update `backend/README.md` shadow section: atomicity/failure/restart
  semantics, ownership.
- `gofmt`, `go vet`, `go test ./...` — race + Postgres integration documented
  as environment-unavailable (no gcc, no docker daemon).
