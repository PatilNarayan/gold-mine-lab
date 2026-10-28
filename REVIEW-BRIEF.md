# Gold Mine Lab — Review Brief

**Purpose of this file:** a single, self-contained context pack to hand to any
LLM (Claude, GPT, Gemini, etc.) for a project review. Read this first, then
inspect the code it points at. Everything below reflects the repo as of
2026-10-28 (commit `6b6bdb9`).

**How to review:** the file pointers in §7 are the load-bearing code paths —
read the actual source, don't trust this summary. §8 lists the questions the
author wants answered. Flag anything where the code contradicts this document.

---

## 1. What it is

A quantitative strategy research and backtesting platform for XAU/USD (gold),
built for honest, reproducible research — not for live trading. Two components:

| Folder | Stack | Size |
|---|---|---|
| `backend/` | Go 1.23 (toolchain 1.25.3) · Gin v1.10 · pgx/v5 + pgxpool · golang-migrate · `log/slog` · coder/websocket | ~18.1k LOC impl, ~7.4k LOC tests |
| `frontend/` | React 18 + TS strict · Vite 5 · Tailwind 3 · React Router 6 · Zustand 5 · TanStack Query 5 · lightweight-charts 5 · ECharts 5 · Zod 3 | ~9.2k LOC src, ~700 LOC tests |

Postgres 15 in Docker (`goldmine-postgres`, host port 5433). API on `:8080`,
frontend on `:5173`. No auth, no broker connectivity, no async queue —
deliberately.

## 2. The pipeline

```
Live/history provider (Tiingo WS or Twelve Data REST)
   → market.DBProvider: sync-on-read into Postgres market_candles + candle_sync
     (validated, session-labeled, provenance-stamped, contiguous coverage)
   → strategy.Registry: ema-cross · rsi-mean-reversion · donchian-breakout
     (+ strategygraph: composed DAG revisions materialized as strategies)
   → signals at bar close → backtest.Engine v2.1
     (fills at NEXT bar open, long-only, adverse slippage, per-fill commission)
   → metrics on NET P&L
   → repository/postgres (parameterized SQL only, atomic run persistence)
   → internal/api (thin handlers, {error:{code,message,request_id}} envelope)
   → React frontend (TanStack Query hooks → http* services, Zod-validated)
```

Optional **shadow engine** replays the frozen EMA20/50 benchmark against the
live quote stream — decisions on confirmed bar closes, hypothetical fills on
admissible quotes, full Postgres persistence (sessions, decisions ledger,
orders, quote-coverage evidence). Simulation only; never real orders.

## 3. Hard invariants (the claims to verify)

These are the project's core promises — a review should try to *break* them:

1. **No look-ahead.** Signal at close(i) → fill at open(i+1). Strategy never
   sees bars > i.
2. **Net P&L accounting.** `grossPnL = (exit−entry)·qty`;
   `netPnL = grossPnL − entryComm − exitComm`; win/loss classified on net;
   `equity_final = initial + Σ netPnL` reconciles exactly with the equity
   curve's terminal point (final-bar liquidation is inside the curve).
3. **Determinism + provenance.** Same inputs → same outputs. Every run records
   dataset sha256 fingerprint + engine/strategy versions (+ graph id/revision).
4. **Undefined = absent = null.** Metrics never report 0/Inf/NaN stand-ins;
   Sharpe deliberately absent (no defined return frequency).
5. **Layer rule.** Handler → Service → Domain → Repository → Postgres.
   No Handler→SQL, no Strategy→I/O, no mock data in the api path.
6. **Immutability.** Graph revisions and frozen study specs are append-only;
   legacy (pre-engine-2.1) runs are preserved, never rewritten.
7. **Honest data.** No fabricated candles — coverage gaps yield empty states;
   live feed is observational only and never enters backtests or persisted
   research; mock mode is dev-only and labeled; API keys never leave the server
   (Tiingo token travels in WS subscribe body / REST header, never a URL).
8. **One clock convention.** `Candle.Time` is a session label — Kyiv civil time
   (EET/EEST) rendered as naive UTC — across historical and live, all
   timeframes. Quote `providerTimestamp` is a real UTC instant. Two coordinate
   systems exist in the shadow engine (`bar_time` labels vs real instants);
   mixing them is a bug class the code explicitly guards against.

## 4. Data & providers

- Historical candles live in Postgres (`market_candles`), backfilled on demand
  by the configured provider's REST history API (Tiingo FX from 2020-01-01);
  `candle_sync` records contiguous coverage. Bootstrap pre-warms 15m/1h/4h/1d;
  1m/5m sync lazily. 4h/1d books aggregate the provider's 1h series into Kyiv
  civil buckets (provider-native 4h/1d rows are UTC-bucketed, incompatible).
- Legacy MT5-export CSVs (`data/`, `data-twelvedata-v2/`) are archival/test
  fixtures only — `CSVProvider` is never wired into the runtime.
- Live feed: Twelve Data (REST poll or WS) or Tiingo FX firehose WS, XAUUSD
  only, mid-price tagged `priceKind=mid`. One upstream subscription fans out
  to all browsers via `GET /api/market/live/stream` (WS) pushing
  `status`/`quote`/`bars` frames; browser bootstraps over REST.

## 5. What's real vs simulated

| Surface | Reality |
|---|---|
| Candles, signals, backtest results | Real computation over real provider data |
| Dashboard / strategy tiles | Latest completed run only — labeled, never aggregated across incomparable runs |
| Live quote panel | Real provider quotes, honest state machine (`disabled/config_error/connecting/connected/stale/disconnected`) |
| Shadow trading | Simulation — decision ledger + hypothetical fills, fully persisted |
| `internal/demo`, `frontend/src/mock` | Deterministic fixtures, tests/dev only, banner-labeled in UI |

## 6. Known limitations (declared, not bugs)

- One real asset (XAUUSD); registry is static in-memory. Frontend mocks
  contain fake TSLA/MSFT assets (dev-only).
- `POST /api/backtests` is synchronous; large 1m windows take seconds.
- No auth, no real-time trading, no portfolio aggregation, no Sharpe.
- Multiple-testing is counted but not corrected (disclosed in study results).
- Docs drift: `KNOWLEDGE.md` / `KNOWLEDGE-GRAPH.md` still describe the
  CSV-based runtime in places; `README.md` + `backend/README.md` are current.
  Point out any other stale claims you find.

## 7. Where the load-bearing code lives

Read these first (repo-relative paths):

**Engine & math** — the correctness core:
- `backend/internal/backtest/engine.go` — execution model, accounting, equity curve
- `backend/internal/metrics/metrics.go` — all metric formulas
- `backend/internal/strategy/emacross.go`, `rsimeanrev.go`, `donchian.go`
- `backend/internal/strategy/indicators/{ema,rsi}.go` — batch + streaming parity
- `backend/internal/strategygraph/{graph,eval,validate}.go` — composite signals

**Data integrity:**
- `backend/internal/market/db.go` — DBProvider sync-on-read, sessionization, validation
- `backend/internal/market/csv.go` — shared `scanRows` validator (tests/legacy)
- `backend/internal/live/{live,twelvedata,tiingo}.go` — provider lifecycle, quote cache, candle books, fan-out hub

**Persistence & schema:**
- `backend/migrations/000001..000019` — schema evolution; shadow tables in 000013–000019
- `backend/internal/repository/postgres/*.go` — parameterized SQL only

**Shadow engine** (subtlest code — two time coordinate systems):
- `backend/internal/shadow/engine.go` + `backend/internal/service/shadowdriver.go`
- `backend/internal/service/quoterecord.go` — quote observation evidence

**API surface:** `backend/internal/api/{router,routes,contract,errors}.go`

**Frontend:** `frontend/src/services/interfaces.ts` + `services/index.ts`
(factory), `services/api/*` (Zod boundary), `types/domain.ts` (contract),
`hooks/*`, `pages/*` (21 routes), `components/charts/*`

**Ops:** `scripts/app.ps1` (full-stack lifecycle), `backend/docker-compose.yml`,
`backend/Makefile`

## 8. Review questions the author wants answered

1. **Correctness:** Can you find a look-ahead path, an accounting error
   (gross vs net, slippage direction, commission double-count), or an equity
   curve that wouldn't reconcile? Try edge cases: gaps, warm-up boundaries,
   zero trades, single-bar windows, DST fall-back (two real hours → one label).
2. **Architecture:** Does the layering hold everywhere? Any I/O or SQL that
   leaked into strategy/engine/handlers? Any place determinism or
   provenance can silently break?
3. **Concurrency:** Live fan-out hub, candle-book updates, shadow engine
   session resolution and retry semantics — races, unbounded buffers,
   double-advance of the stream, session-state leakage across sessions?
4. **Data honesty:** Any path that could fabricate candles, fake a live price,
   mix providers' coverage, silently fall back between transports, or leak
   an API key into a log/URL/response?
5. **Shadow engine:** Are `missed` vs `unobservable` correctly distinguished?
   Can a current quote ever fill an old signal? Is the decision→order
   invariant (`actionable ⇒ order_id`) enforced end-to-end?
6. **API contract:** Do frontend Zod schemas (`types/domain.ts`) and backend
   DTOs (`api/contract.go`) actually agree? Error envelope consistent?
7. **Migrations:** Up/down pairs correct and safe on real data? Any migration
   that could break the immutability or session-scoping rules?
8. **Testing:** Where are the gaps? What invariant lacks a failing-test-proof?
   (Existing: unit tests for indicators/strategies/engine/metrics/validators/
   live providers; integration tests behind `//go:build integration`.)
9. **Frontend:** Is server state kept out of Zustand? Any trading math in UI
   components (should be none — display only)? WS frame handling consistent
   with snapshot semantics?
10. **Docs:** Where does `KNOWLEDGE.md`/`KNOWLEDGE-GRAPH.md` disagree with the
    code as it exists now?

## 9. Repo facts for orientation

- 19 migrations; tables: assets, strategies, strategy_signals, backtest_runs,
  backtest_trades, backtest_metrics, comparisons, walk_forward_*,
  strategy_graphs(+revisions), signal_research, research_studies,
  market_candles, candle_sync, quote_observations, shadow_* (sessions,
  decisions, orders, positions), feed_events, coverage evidence.
- Strategies: `ema-cross` v1.0.0 (fast 20/slow 50), `rsi-mean-reversion`
  v1.0.0 (14/30/70, long-only), `donchian-breakout` v1.0.0, `graph:<uuid>:v<n>`
  materialized revisions.
- Research governance: pre-registered studies (draft→frozen→exploratory→
  holdout_consumed) with SHA-256 spec hash + holdout window reservations that
  analytical endpoints must reject overlapping (app-level, not access control).
- `docs/` contains 11 dated research reports (evaluations, audits, readiness).
- Run: `docker compose up -d postgres` → `go run ./cmd/server` →
  `npm run dev`; or `scripts/app.ps1 start` for all three.
- Tests: `go test ./...`; `go test -tags=integration ./...` (needs DB);
  `npm test`, `npm run typecheck`, `npm run lint`.

## 10. Suggested prompt for the reviewing LLM

> You are reviewing a Go+React quantitative backtesting platform. The attached
> REVIEW-BRIEF.md describes the system's invariants and architecture; the repo
> is available for you to inspect. Your job: try to falsify the invariants in
> §3, starting with the files in §7, and answer the questions in §8. Report
> concrete findings with file:line evidence — ranked by severity — not general
> advice. Distinguish real bugs from the deliberately-declared limitations in
> §6. If the code is correct where it matters most, say so explicitly.
