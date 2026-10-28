# Product-Depth Pass — Design (approved by delegation)

**Context:** Gold Mine Lab, personal single-user quant research terminal
(XAU/USD). User: "just me, on this machine", "do all your best, finish,
don't ask." Decision: functional depth + polish inside existing seams —
NO auth/deploy/multi-user, no rewrites, strategy math unchanged.

**Environment limits:** Windows, no gcc (`-race` unrunnable), Docker daemon
down (Postgres integration tests unrunnable), node v18 works, go1.25.3 works.
Working tree already has user's uncommitted changes — do not revert.

**Done earlier this session:** single-writer `shadow.Runtime`
(internal/shadow/runtime.go), atomic `CommitBar` (postgres + memStore),
`postgres.AcquireShadowOwnership` advisory lock, driver/service/API rewired,
`runtime` block on /api/shadow/status, ordered shutdown in main.go,
runtime_test.go (16 tests). All backend tests + vet + gofmt + frontend
typecheck pass.

## Workstreams

### W4 Product hygiene (quick)
- Untrack committed binaries: `backend/datasync.exe`, `backend/bin/` → .gitignore.
- Fix docs drift: KNOWLEDGE.md/KNOWLEDGE-GRAPH.md note current runtime
  (DBProvider+live+runtime), point at README as authoritative.
- Mock assets: keep (dev-only, banner-labeled) — document; don't remove
  (used by tests). Verify no fake asset leaks into api mode.
- Write verification commands into backend README (go test, race w/ gcc,
  integration w/ GOLDMINE_TEST_DSN, frontend typecheck/test/lint).

### W3 Runtime health in UI
- Surface `runtime` block (already additive in status response + zod schema
  done) on Shadow page: state badge (starting/running/unhealthy/stopped),
  reason, events submitted/processed/rejected/dropped.
- Files: frontend/src/pages/Shadow.tsx (+ maybe useShadow hook — check).

### W2 Real realtime client
- Implement RealtimeClient (services/websocket/realtime.ts) connecting to
  VITE_WS_URL... NOTE: actual live WS endpoint is /api/market/live/stream —
  check frontend/src/services/websocket/realtime.ts + hooks/useLiveStream.ts
  + services/api/httpLive.ts to see intended shape; wire createServices to
  real client in api mode; drive Market Live chart from stream frames
  (status/quote/bars) instead of polling.
- Keep mock mode noop; update README known-limitations.

### W1 Portfolio view (biggest)
- internal/portfolio is empty scaffold. Define scope honestly: aggregate
  COMPLETED backtest runs into portfolio-level metrics — but keep
  "independent runs ≠ portfolio" honesty: present aggregate analytics as
  analytics, not a single equity claim.
- Backend: service/repository aggregation or pure compute endpoint
  (e.g. GET /api/portfolio/summary?strategy=&asset=): per-strategy run
  counts, win rate, median/worst return, best/worst run, total trades —
  computed from backtest_runs+metrics (repository SQL or in-memory over
  ListRuns). Frontend: Portfolio section on Dashboard or new /portfolio page.
- Simpler preferred path: service method composing existing repo reads —
  check repository.go for Runs listing + metrics joins before designing.

## Verify per workstream
go build ./... && go vet ./... && gofmt -l (touched dirs) &&
go test -count=1 ./... && frontend: npm run typecheck/lint/test.

## Completion definition
All 4 workstreams implemented + checks green + README updated + final
summary of what shipped, what couldn't be verified, and next steps.
