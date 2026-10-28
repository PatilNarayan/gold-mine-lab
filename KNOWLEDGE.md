# Gold Mine Lab — Project Knowledge Base

> **Status note (2026-10-28):** parts of this file predate the
> provider-synced Postgres candle store, the live feed, and the
> single-writer shadow runtime. Where it conflicts with `README.md` and
> `backend/README.md`, those are authoritative.

Everything about what exists, why it exists, and how the pieces connect.
Update this file when the architecture or use-cases change.

## What Gold Mine Lab is

A quantitative **strategy research and backtesting terminal**. Researchers
load historical market data, define trading strategies, generate signals,
backtest them over history, and inspect metrics — all from one web UI.
Focused on XAU/USD (gold spot) today; asset-agnostic by design so XAGUSD,
MSFT, TSLA, EURUSD, … plug in when their datasets arrive.

## Repository layout

```
GoldMineLab/
├── frontend/    React 18 + TS + Vite + Tailwind — the research terminal UI
├── backend/     Go + Gin + PostgreSQL — the research API + engine
├── data/        Market CSVs (gitignored, ~470 MB) — real XAUUSD history
└── README.md    Quick start
```

Both apps are separate projects sharing one REST contract (`VITE_DATA_MODE=api`).

## The pipeline (what actually happens when you use it)

```
data/XAU_*_data.csv  (HuggingFace mirror of Kaggle XAUUSD history, 2004-06-11→2025-09-30)
   ↓  CSVProvider: validated streaming reads (Tail for charts, Range+warmup for backtests)
Strategy registry    (ema-cross + rsi-mean-reversion — pure math, no I/O)
   ↓  signals persisted to strategy_signals (idempotent)
Backtest Engine      (next-candle-open fills, 1 position, commission+slippage)
   ↓  metrics (net P&L, win rate, PF, max DD…) + equity curve
PostgreSQL           (6 tables; migrations auto-apply at startup)
   ↓  REST API (thin handlers → service → repos)
React frontend       (charts, tables, run-history — reads, never computes)
```

## What is real vs. simulated — honesty map

| Surface | Source | Honesty |
|---|---|---|
| Market candles, EMA overlays, volume | **Real** XAUUSD CSVs from a HuggingFace mirror of the Kaggle MetaTrader export | Real prices |
| BUY/SELL signal markers + signals table | **Real** — both strategies evaluated over real candles | Real crossovers/threshold crosses |
| Backtest trades/metrics/equity curves | **Real** — engine replayed over real candles | Real simulated results (a real backtest, not fabricated) |
| Strategy list / performance tiles | Real — latest completed run per strategy | Real |
| Market status | "Historical" | Dataset is frozen history, not live feed |
| Dashboard cards | **Latest completed run** only | Independent runs ≠ portfolio |

Nothing on screen is random filler anymore. The old deterministic `demo`
package still exists but is only used by tests — production path is real.

## Data freshness

Market endpoints serve the dataset's **actual last candle** (currently
2026-01-30, XAUUSD ≈ $4,889) — there is no simulated "now" and no invented
price. Market status reports `Historical`; V1 has no live feed. Backtests use
their explicit date range.

## Backend module map (backend/)

| Package | Role | Use case |
|---|---|---|
| `cmd/server` | Entrypoint wiring only | boot, shutdown |
| `internal/config` | Env config, fail-fast validation | required vars & sane defaults |
| `internal/database` | pgxpool + ping | connectivity & health |
| `internal/api` | Router, middleware, error envelope, handlers, wire DTOs | REST contract |
| `internal/market` | Asset/Candle/Timeframe + `DataProvider` + `CSVProvider` | real historical data |
| `internal/strategy` | Strategy contract, `EMACross`, static `Registry`, `indicators/EMA` | pluggable strategy math |
| `internal/backtest` | Deterministic engine (see execution model) | replay strategy over history |
| `internal/metrics` | Metric formulas | quantify results |
| `internal/repository` + `/postgres` | DB access only (parameterized SQL) | persistence seam |
| `internal/service` | Assembles pipeline; implements `api.*Service`; `Bootstrap` | orchestration |
| `internal/demo` | Legacy deterministic service | kept for tests only |
| `migrations/` | Embedded golang-migrate SQL + `Up()` | schema versioning |

Layer rule: Handler → Service → Domain/Engine → Repository → Postgres.
Never Handler→SQL, never Strategy→I/O.

## Key design decisions (and why)

- **Next-candle-open fills.** Signals compute on bar *i* close and fill at bar
  *i+1* open — same-candle execution would be look-ahead bias.
- **Date-range convention.** `startDate` inclusive, `endDate` inclusive at
  date granularity → translated to `[start, endDate+1day)` UTC exclusive — a
  candle at midnight on the day after endDate is never included.
- **Warm-up windowed reads.** `CSVProvider.Range(from, toExclusive, warmup)`
  returns pre-window bars for indicator convergence (ema-cross asks for
  `3× slow_period` via the `Warmupper` interface — parameter-derived, not a
  fixed constant). Signals are only *acted on* inside the requested range.
- **Shared validation.** Charts (`Tail`) and backtests (`Range`) use the same
  `scanRows` policy: explicit header recognition, file:line errors, NaN/±Inf
  rejected, OHLC integrity, strictly ascending, no duplicates.
- **Net P&L accounting (engine 2.1).** `grossPnL = (exitFill − entryFill)·qty`;
  `netPnL = grossPnL − entryCommission − exitCommission`; win/loss classified
  on **net**; `equity_final = initial + Σ netPnL` reconciles exactly with the
  equity curve's terminal point (final liquidation is inside the curve).
- **Experiment provenance.** Every run persists `engine_version`,
  `strategy_version`, `dataset`, params, window, capital, costs (migration
  000002). Pre-2.1 rows are marked `engine_version='legacy'` — their `pnl`
  was gross and they lack cost decomposition; they are preserved, not
  retroactively "corrected".
- **Latest-run dashboard.** Independent runs (different params, windows,
  capital) are experiments, not one portfolio — the dashboard shows the
  experiment count plus the *latest completed run's* metrics and equity
  curve, labeled as such.
- **Latest-run strategy summaries.** `StrategyPerformance` likewise reports
  the experiment count + the latest completed run's own metrics with its ID,
  timeframe and date range — never summed P&L or averaged win rates across
  incomparable runs. Comparable-strategy analysis is a separate future
  research feature.
- **Dataset provenance.** Every run records `dataset` as
  `<asset>:<tf>@sha256:<64-hex>` — the full SHA-256 of the exact CSV
  used, cached per file per process (`CSVProvider.Fingerprint`). Runs
  before 000004 keep their recorded 16-hex identity; nothing is
  rewritten. The release data is a HuggingFace mirror
  (`ZombitX64/xauusd-gold-price-historical-data-2004-2025`) of the Kaggle
  MetaTrader XAUUSD export by novandraanugrah, covering 2004-06-11 through
  2025-09-30. The expected daily fingerprint is
  `xauusd:1d@sha256:5335b0dc2df9f9e980f4a6d32f70711f694eff7e461e092bb9e81e0fe619d802`.
  Timestamps are naive date-only values stored under the project's UTC
  convention — the upstream broker timezone is NOT independently
  established.
- **RSI (Wilder).** `indicators.RSI` / `RSIState`: seed = SMA of first
  `period` changes, then `avg = (prevAvg·(p−1)+x)/p`. Undefined (NaN) for
  index < period; avgLoss==0 → 100 (avgGain>0) or 50 (both zero = constant
  prices). Batch and stream are identical.
- **Comparisons.** `/compare` runs 2–4 strategies under identical
  assumptions (window/capital/sizing/costs/dataset/engine) — per-leg params
  only differ. Optional out-of-sample window must be strictly later; each
  leg's trades/metrics belong only to its own window (warm-up may precede
  it). Results carry per-run fingerprint + engine/strategy versions; no
  combined portfolio curve; <30-trade legs trigger a small-sample warning.
  Every leg×window is validated before anything is stored (an invalid leg →
  400, nothing persisted). Status: `completed` = every member run completed;
  `partial_failed` = some completed, some failed (failed runs stay attached
  with their error); `failed` = all failed. Max 4 strategies (8 runs with
  OOS). Warm-up bars before a window feed indicators only: signals before
  `WindowStart` are dropped and the OOS run starts flat in its own engine
  run. `gross_profit`/`gross_loss` are Σ NET P&L of winners/losers (after
  commissions); `total_commissions` is null on runs that predate it.
- **Walk-Forward.** `/walkforward` replays each leg's FIXED params over
  rolling folds (IS window, then OOS window, advanced by step; default
  step = OOS length; `step < oos` rejected so OOS windows never
  overlap). Fold boundaries are half-open internally `[start, end)`:
  OOS starts the day after IS ends — the windows never share a candle
  date (persisted/displayed dates are inclusive, `isEnd` = last IS
  day). Months add via `addMonthsUTC` anchored on the ORIGINAL range
  start with month-end clamping (Jan 31 + 1m → Feb 28/29) — no day
  drift. Fit check compares exclusive ends against `endDate+1d`; a
  partial tail fold is dropped, never shortened. Member runs are
  ordinary `backtest_runs` rows with `walk_forward_id` + `sample`,
  linked via `walk_forward_folds`. Statuses mirror comparisons, plus:
  a failed `SetFoldResult` write is logged and forces
  `partial_failed`/`failed` — never "completed" with unsaved fold
  records — and a mid-setup failure (status transition or fold insert)
  best-effort marks experiment + created folds `failed` ("interrupted")
  so nothing stays pending/running. A fold's referenced run must load
  and a completed run must have metrics, else GET errors instead of a
  misleading partial response. Per-strategy OOS summaries only:
  completed/failed/positive fold counts, median + worst return, worst
  maxDD, total trades, <30-trade folds, `inconclusive` when most folds
  are thin; fold curves shown independently, never merged.
- **rsi-mean-reversion v1.0.0.** BUY = RSI crosses UP through oversold;
  SELL = RSI crosses DOWN through overbought (exit only, long-only). A cross
  fires once — staying beyond a threshold does not re-signal. Params:
  `rsi_period` [2,100], `oversold` [1,49], `overbought` [51,99], os<ob.
  Warmup = `3×period+2`. Same timeframe set as ema-cross.
- **Streaming evaluation.** Strategies can implement `strategy.Streamer`
  (O(1) state per bar); the engine falls back to prefix `Evaluate` —
  correctness first, efficiency when available.
- **Atomic completion.** `CompleteRun` writes trades + metrics + equity curve
  + status in one transaction; failures mark `failed` with a safe message.
- **Idempotent signals.** UNIQUE(strategy_id, asset_id, timestamp) +
  ON CONFLICT DO NOTHING → regenerate freely on every boot.
- **UUIDs** for generated rows (runs/trades/signals/metrics); readable slugs
  for assets/strategies (`xauusd`, `ema-cross`).
- **golang-migrate embedded** — schema travels with the binary; `migrations.Up`
  runs at startup; `ErrNoChange` is fine.
- **Pre-registered research studies (Phase 12).** Studies move from `draft` to
  `frozen`, then `exploratory_complete`, and finally `holdout_consumed`.
  Draft edits are allowed; frozen specifications are immutable. Freeze stores
  a SHA-256 hash of the normalized, ordered study specification, dataset
  fingerprint, asset/timeframe, and both date windows. It also reserves the
  inclusive holdout dates for that asset/timeframe. Freeze checks are
  serialized with a PostgreSQL transaction advisory lock; reservations stay
  active while frozen or exploratory-complete and are released only after
  successful confirmatory consumption. Execution records append; failed
  exploratory and confirmatory runs do not advance the lifecycle and may be
  retried. A repeated confirmatory request after consumption returns the
  saved study without creating another execution.
- Study comparison counts record candidate configurations, immutable graph
  revisions, horizon cells, and (when enabled) regime cells. Counts explicitly
  say “this is how many comparisons were looked at; no correction applied”:
  the application does not perform multiple-testing correction. The
  preregistered primary metric at the primary horizon is labeled `primary`;
  remaining horizons and regime-conditioned cells are `secondary`. A
  confirmatory outcome is descriptive evidence, not proof of profitability or
  removal of selection bias.
- Application analytical endpoints reject evaluation windows that overlap an
  active study holdout. Warm-up candles before the requested evaluation range
  remain available and are not considered an overlap. Confirmatory runs may
  bypass only their owning study's reservation. This is an application-level
  policy, not an access-control boundary: researchers can still inspect or
  analyze the raw CSV files outside the application, an unavoidable escape
  hatch that must be disclosed when interpreting results.

## Execution model (the contract of every backtest)

1. Signal at candle **close** → fill at **next candle open**.
2. Single long position only: BUY opens when flat, SELL closes when long.
   No shorts/leverage/pyramiding/partial exits/trailing stops (V1).
3. Slippage adverse (`±s%` on fill price), commission `%` of notional per fill.
4. `positionSize` = % of current equity per trade.
5. Trade P&L: `gross = (exit − entry)·qty`; `net = gross − entryComm − exitComm`.
   `pnl` stored and displayed is **net**; gross + both commissions stored
   separately (engine ≥2.1). Legacy runs (pre-migration-000002) keep their
   gross `pnl` and NULL cost columns — they are marked, not recomputed.
6. Mark-to-market equity each bar; final open position liquidated at last
   bar's close inside the curve → terminal equity point = realized equity =
   `initial + Σ netPnL`.
7. Fully deterministic: same input → same output.

## Metric formulas

netPnl = final − initial · returnPct = netPnl/initial · winRate = wins/closed
(classified on **net** trade P&L) · avgTradePnl = netPnl/closed ·
profitFactor = grossWin/grossLoss (0 if no losses) ·
maxDD = max((peak−equity)/peak) over the curve.
**Sharpe intentionally absent** — no defined return frequency yet.

## Strategy & engine versioning

- `backtest.EngineVersion = "2.1.0"` — net-P&L accounting + post-liquidation
  terminal equity.
- Strategy versions come from `Strategy.Version()` at execution time (ema-cross
  = 1.0.0). Changing strategy logic must bump its version so historical runs
  keep their identity.
- `strategies.supported_timeframes` (JSONB): per-run timeframe is a choice
  within the advertised set — ema-cross: 15m/1h/4h/1d, default 1h.
- `EMA` seeds at the first close, `k = 2/(n+1)`; warm-up = `3× slow_period`
  fetched pre-window; first `slow` streamed bars emit NEUTRAL only.

## Risk & research metrics (Phase 6a)

- **All risk metrics are computed on NET trade P&L** (after commissions and
  slippage), in `internal/metrics/metrics.go`, from the deterministic
  chronological trade order — no DB access in calculation.
- **Conventions:**
  - `avg_win` = mean of net winners; `avg_loss` = mean net-loser magnitude
    (positive). `payoff_ratio` = avg_win / avg_loss (magnitude comparison).
  - `expectancy` = net_pnl / closed-trade count (currency, equals avg trade).
  - `max_consecutive_losses` = longest streak of net < 0 trades in exit order;
    break-even trades reset the streak; terminal liquidation counts.
  - `bars_in_market` / `eligible_eval_bars` / `time_in_market_pct` — bar-based
    exposure: position open on the bar within the evaluation window only
    (no warm-up bars, entry bar counts, exit bar doesn't, liquidation bar
    counts). It is NOT wall-clock time.
  - `recovery_factor` = net_pnl / max_drawdown_amount (currency ratio; may be
    negative). Max drawdown amount = peak-to-trough $ from the equity curve.
- **`profit_factor` follows the same undefined rule**: gross_loss > 0 →
  ratio (a valid 0 when there is zero profit); gross_loss == 0 → absent row →
  API null (never Inf, never fake 0/99). The API only surfaces a stored
  `profit_factor` when a positive `gross_loss` row confirms the ratio was
  defined — pre-gross-metric legacy rows can't prove that, so they show null.
- **Undefined = absent row = API null.** EAV `backtest_metrics` means no
  migration was needed; legacy runs simply lack the rows and the API/UI show
  null/"—" for them (never 0, never Inf/NaN).
- **Walk-forward summary aggregation (no pooling):** median OOS expectancy,
  median OOS time-in-market %, worst OOS consecutive-loss count across folds,
  `missing_metric_folds` (folds where all risk metrics are unavailable, i.e.
  pre-6a runs), `<30-trade folds` still flags thin evidence. Inconclusive
  verdict rules unchanged.
- **Sharpe/Sortino deliberately absent:** return frequency and annualization
  are not yet defined.
- Derived metrics share the run's provenance (dataset fingerprint,
  engine/strategy versions); no version bump — historical runs are not
  recalculated.

## Strategy graphs (Phase 6b)

- **What:** `internal/strategygraph/` composes registered strategies with
  explicit operators into a directed acyclic graph that produces ONE composite
  signal per bar, fed into the SAME `Execute` engine (next-candle-open fills,
  sizing, commissions, slippage, metrics, fingerprint — no separate simulator).
  Declarative JSON definitions only — no generated or user-supplied code.
- **Node types:** `strategy` (a source: registry strategy id + params; each
  node owns an INDEPENDENT streaming state via `Streamer.NewStream()` — two
  nodes of the same strategy with different params do not share state);
  `and` (>=2 inputs: emits a direction only when EVERY input is non-NEUTRAL and
  agrees, score = min of input scores); `or` (>=2 inputs: all NEUTRAL -> NEUTRAL;
  a single actionable direction -> that direction, score = max; conflicting
  directions -> NEUTRAL); `threshold` (exactly 1 input, t in [0,1]: pass
  BUY/SELL when score >= t else NEUTRAL — score is signal STRENGTH, not a
  probability of profit); `output` (exactly 1 input, exactly one per graph —
  marks the composite signal; it does NOT execute trades).
- **Validation (`Validate`)** rejects before anything is persisted: duplicate
  node ids, unknown types/strategies/params, unsupported asset/timeframe,
  missing/invalid/duplicate edges, cycles, zero or multiple outputs, arity
  violations, disconnected or output-unreachable nodes, >32 nodes / >64 edges,
  missing or non-finite thresholds. Errors are structured `{nodeId,message}`.
  Topological order is deterministic (lexicographic ties), >=1 source required.
- **Evaluation:** per-candle chronological; source streams configured once via
  `Configurable`, evaluated in topo order; warm-up = max(source warm-ups);
  deterministic — identical definition+data => identical signals; only
  `Streamer` strategies may be sources (O(n) streaming, never future bars).
  Composite signal metadata carries `{graphId, graphRevision}`.
- **Persistence (migration 000005):** `strategy_graphs` (id, name, description,
  status) + `strategy_graph_revisions` (graph_id, revision, definition jsonb,
  definition_hash sha256). Revisions are IMMUTABLE — editing appends
  `revision+1`; every run records the exact revision via strategy slug
  `graph:<uuid>:v<n>`, so provenance/hash survive restarts (the adapter is
  re-registered lazily on each graph backtest).
- **API:** `GET /api/strategy-graphs` · `POST /api/strategy-graphs/validate` ·
  `POST /api/strategy-graphs` · `GET /api/strategy-graphs/:id` ·
  `GET /api/strategy-graphs/:id/versions` · `POST /api/strategy-graphs/:id/versions`
  · `POST /api/strategy-graphs/:id/versions/:version/backtests` (definition
  comes from the saved revision, never from the client body).
- **Strategies list:** materialized graph revisions appear as `graph:…:v<n>`
  rows — distinguishable from built-ins by the `graph:` prefix.
- **Donchian breakout (Phase 8):** `donchian-breakout` v1.0.0 — long-only
  channel breakout. BUY when close > highest high of the PRECEDING N candles,
  SELL when close < lowest low of preceding N (exit only, never opens a
  short; engine is long-only anyway). `lookback_period` default 20, int,
  [5,200]. The current candle's own high/low never enters its channel.
  A breakout is a continuing condition — repeated BUY/SELL while new
  extremes persist, NOT a one-time cross. Score = breakoutDistance /
  (channelWidth + breakoutDistance) in [0,1) — a heuristic of how far the
  close cleared the channel, NOT profit probability; zero-width channel +
  real breakout → 1. Stream path uses a monotonic-deque rolling extrema
  (amortized O(1)/bar); batch Evaluate recomputes the window — equivalent
  by test. Warm-up = lookback bars. Works as a graph source node, in
  comparisons, walk-forward, and the signal inspector (metadata carries
  upper/lower/width/lookback/close).
- **Signal inspector (Phase 7):** `POST /api/strategy-graphs/:id/versions/:version/diagnostics`
  replays the saved revision's own streams over `[startDate,endDate]` and
  reports, per node, BUY/SELL/NEUTRAL counts, actionable %, avg score for
  actionable outputs, and top reasons — plus a bounded candle-level trace
  (`traceLimit` default 200, max 2000; evaluated-window cap 250k bars).
  Read-only: no backtest_runs, no trades, no persistence. Tracing uses the
  SAME evaluation path (`TraceStreamer.NextWithTrace` on graphStream) so
  traced and untraced outputs are identical by construction. Warm-up candles
  feed sources but are never counted. Operator reasons are stable strings —
  `and: input neutral` vs `and: inputs disagree`, `or: all inputs neutral` vs
  `or: conflicting directions`, `threshold: input neutral` vs
  `threshold: <ACTION> score <x> below <t>`. Response carries dataset
  fingerprint + last candle for reproducibility.
- **Research integration (Phase 6c):** `resolveStrategy` resolves ANY engine
  strategy id — registry hit first, else a strict `graph:<uuid>:v<n>` slug is
  lazily rebuilt from its immutable saved revision (`ensureGraphStrategy`), so
  comparison and walk-forward legs can reference graph revisions even after a
  restart (no startup pre-load of every revision). Graph legs reject
  client-supplied parameters — node params live only in the revision — and
  runs carry `{graphId, graphRevision}` provenance instead. Revision saves
  are atomic: `CreateRevisionWithStrategy` commits the revision row and its
  materialized strategies row in one transaction (unique-violation retry on
  concurrent creates), so no revision can exist without a runnable identity.
- **Limits (MVP):** 5 node types only; single-asset/timeframe per run;
  operators take the max of source warm-ups, not per-edge delays; a graph
  result says nothing about profitability — it is one more hypothesis to test,
  not a strategy that is "proven" because it is composed.

## Database schema

`assets`, `strategies` (slugs, `supported_timeframes` jsonb), `strategy_signals`,
`backtest_runs` (+`equity_curve` jsonb, `engine_version`, `strategy_version`,
`dataset`), `backtest_trades` (+`gross_pnl`, `entry_commission`,
`exit_commission`), `backtest_metrics` — see `migrations/000001` and
`000002_accounting_consistency`. Plus `strategy_graphs` +
`strategy_graph_revisions` (`migrations/000005_strategy_graphs`). Indexes per access pattern:
signals (strategy,asset,ts), runs (strategy,asset,created), trades (run_id),
metrics (run_id).

## Frontend module map (frontend/)

| Area | What it does |
|---|---|
| `src/services/` | Interface seam: `http*` impls hit the Go API; `mock*` kept for offline dev; factory picks via `VITE_DATA_MODE` |
| `src/types/` | Zod schemas mirroring the API contract exactly |
| `src/mock/` | Deterministic seed generators — only in `mock` mode |
| `src/stores/` | Zustand UI state (asset, timeframe, theme, drafts) — never server data |
| `src/hooks/` | TanStack Query hooks — all server data |
| `src/components/` + `pages/` | Charts (Lightweight-Charts + ECharts), tables, forms, layout |
| `?mock=` | scenario switch for loading/empty/error demos (mock mode only) |

## Environment & ops notes

- Dev Postgres: docker-compose service `goldmine-postgres`, **host port 5433**
  (a local Postgres service owns 5432).
- `frontend/.env` → `VITE_DATA_MODE=api`, `VITE_API_URL=http://localhost:8080`
  — gitignored; never commit.
- `backend/.env.example` documents every var; `MARKET_DATA_DIR` is required.
- Fetch market data with `./scripts/fetch-data.sh` or
  `.\scripts\fetch-data.ps1`; verified CSVs live in `data/` (gitignored —
  never commit the roughly 470 MB of datasets). `GML_DATA_BASE_URL` can
  point the fetchers at a compatible mirror.
- GitHub repo: `github.com/PatilNarayan/gold-mine-lab` (branch `main`).

## Verify / test commands

```bash
cd backend && go build ./... && go vet ./... && go test ./...
DATABASE_URL=… MARKET_DATA_DIR=… go test -tags=integration ./...
cd frontend && npm run typecheck && npm run lint && npm test && npm run build
```

## Roadmap pointers (not built — by design)

Auth · live market data / websockets · broker execution · async backtest
queue (POST is synchronous today) · more strategies/assets · DB-driven
strategy registry · Sharpe/benchmark metrics · frontend auth gating.

## Signal Research Lab (Phase 9)

Cross-strategy signal research (`service/signalresearch.go`, `api/routes.go` registerSignalResearchRoutes, `internal/repository/postgres/signalresearch.go`, migration `000006`):

- **Candidates**: 2–8 legs (`srMaxLegs`), each a built-in strategy ID (params applied via `Configurable.Configure`) or `graph:<uuid>:v<n>` (exact immutable revision, params rejected via `graphLegParameters`). Shared asset/timeframe/dataset/dates.
- **Shared replay**: ONE `data.Range` fetch (warmup = max across candidates). Each candidate gets its own `NewStream()`; every candidate sees every candle in timestamp order. `actions[leg][wi]`/`scores[leg][wi]` recorded only for `wi >= windowStart` — warm-up bars are streamed but never counted (`evaluatedBars`/`warmupBars` reported).
- **Stats per candidate**: BUY/SELL/NEUTRAL raw counts, actionable %, BUY/SELL **episodes** (consecutive same-direction runs — NOT trades), avg score over actionable bars, strategy version / graph revision id.
- **Pairwise**: timestamp-aligned counts — bothBuy, bothSell, sameDirection, opposite (BUY↔SELL conflict), oneActionable, bothNeutral, coActionable. `agreementPct`/`conflictPct` are percentages **of co-actionable bars** and are `null` when coActionable = 0. NEUTRAL/NEUTRAL is NOT agreement.
- **Forward study** (when `request.eventStudy`): BUY episodes only, horizons 1/5/10/20 (`srHorizons`). Event = first BUY bar of each episode; entry = NEXT candle's open; exit = close h candles after signal bar. Incomplete horizons are excluded, never shortened. Returns are gross % price moves — no costs, sizing, management; NOT P&L, NOT predictions. No forward SELL study (SELL = long-exit semantics).
- **No look-ahead**: signals come only from streams fed past+current candles; outcomes never feed back into generation/config. Forward study is a separate reporting layer.
- **API**: `POST /api/signal-research` (execute+persist), `GET /api/signal-research` (summary list — candidates/pairs/forward nulled), `GET /api/signal-research/:id`. Result persisted as JSONB on `signal_research` (status pending→completed/failed; failures persist honestly). Summary records only — never per-candle signals. `srMaxEvalBars` = 250_000.
- Frontend: `/research` + `/research/:id` (`pages/SignalResearch.tsx`), services `signalResearch` (http + deterministic mock), `useSignalResearch*` hooks.

**Persistence hardening (Phase 9.1)**: the dataset fingerprint is obtained BEFORE the row is created — no provenance, no persisted analysis. Cancellation mid-replay marks the row `failed` via `failSignalResearch` (bounded context detached from the cancelled request; cleanup errors logged separately, never masking the cause). Retrieval validates stored JSON: malformed result/request and a completed record without a result are integrity errors (500), never zero-valued. Unknown candidate IDs return 400 (nil-resolve previously panicked).

## Regime-aware signal research (Phase 10)

`internal/regime` — deterministic, streaming market-regime classifiers fed once per candle, independent of HTTP/Postgres/strategy execution. `DefinitionVersion = "regime-v1"` is stored on every regime-enabled analysis.

- **Trend** (per candle close): EMA-50 and EMA-200 of closes. `uptrend` = close > EMA200 AND EMA50 > EMA200; `downtrend` = close < EMA200 AND EMA50 < EMA200; `transition` = every other combination (NOT a proven range); `unknown` = <200 bars of history. Warmup = 200.
- **Volatility**: TR = max(high-low, |high-prevClose|, |low-prevClose|); Wilder ATR-14 (SMA seed, recursive after); `ATRPercent = ATR/close*100`. Baseline = EMA-100 over **prior completed** ATRPercent values only — the current observation joins the baseline AFTER its own label is decided. `low` < 0.8×baseline, `high` > 1.2×baseline, `normal` in between (inclusive), `unknown` until ATR + 100 baseline points exist (~114 bars). Multipliers are research conventions, not tuned. Labels are timeframe-relative.
- **Integration** (`service/signalresearch.go`): `regimeAnalysis` flag on `POST /api/signal-research`; default off preserves the old response exactly (all regime fields omitempty). When on: `maxWarmup = max(strategyWarmup, regime.WarmupBars)`; one shared classifier pass during the SAME replay; labels recorded only for evaluated bars.
- **Per-regime outputs**: `candidates[].byTrend/byVolatility` (same stats incl. episodes over each bucket's chronological subsequence), `pairs[].byTrend/byVolatility` (same agreement definitions; pct null when no co-actionable bars), `forward[].byTrend/byVolatility` (regime at the EPISODE'S signal bar; same entry/exit/exclusion rules). No trend×volatility matrix — two independent dimensions.
- Frontend: builder checkbox "Enable Market Regime Analysis"; detail adds Strategy-by-regime, Agreement-by-regime, Forward-outcomes-by-regime tables + regime definitions version in provenance.
- Hardening also in this commit: `strategyIds` now marshals `[]` not `null` for result-less rows (old failed rows previously broke the list schema).

## Statistical validation in signal research (Phase 11)

Adds a matched-baseline + block-bootstrap layer to the Signal Research forward
study. No strategies, accounting, or engine semantics change.

- **Baseline (`baseline-v1`)** — for each (candidate, horizon), the SAME gross
  forward return formula (next-open entry, close-h exit) is computed on EVERY
  eligible evaluation bar. "Baseline" = all eligible bars (events ∪ controls);
  events are a subset of the baseline, so event-vs-baseline differences are
  descriptive only — never an independence claim. Reported per cell: event /
  control / baseline counts, event+baseline mean & median, diff in means and
  medians (pp), positive-% for both, worst event return.
- **Moving-block bootstrap (`mbb-v2`)** — the aligned chronological
  observation sequence (event flag + return + regime labels, one record per
  eligible bar) is resampled in blocks of `horizon+1` bars. Each block's
  start is uniform over the n−blockLen+1 valid complete-block positions;
  blocks are appended until the replicate covers n observations and only
  the final block is truncated, so every replicate is exactly n long.
  (v1 sampled starts anywhere and truncated every overhang block, giving
  variable-length replicates; saved `mbb-v1` records keep their original
  metadata + results.) Per replicate, diff = mean(event) − mean(control);
  replicates missing either group are skipped. 95% percentile interval
  over 1000 replicates. Prefix sums keep each block contribution O(1).
  Seed = fixed base `0x5eed11` × FNV-1a of (strategyID, horizon, regime
  cell) — identical requests reproduce identical intervals.
- **Minimum-sample rules** — a CI is only computed when ≥10 events AND
  ≥50 controls AND ≥80% replicates are valid; otherwise `insufficient_data`
  (descriptives still returned, never a fabricated interval). An interval
  spanning zero is `inconclusive`; excluding zero is `available` — an
  exploratory difference only, never "profitable".
- **Estimands** — `diffMean`/`diffMedian` = event − ALL-bar baseline
  (descriptive; events ⊂ baseline). `diffMeanControl`/`diffMedianControl` =
  event − non-event controls; this is the point estimate the CI describes.
  `controlMean`/`controlMedian` are exposed separately. Old records lack
  the control fields — they are added, never redefined.
- **Regime-conditioned benchmark** — with regimeAnalysis on, each regime
  cell compares regime events to same-regime non-event controls
  (regime at each bar's own close). The bootstrap resamples the FULL
  chronological sequence and applies the regime filter INSIDE each
  replicate, so blocks keep true market-time adjacency (filtering first
  would fabricate adjacency across regime gaps). Both dimensions, no
  cross-product.
- **Multiple-search caution** — repeated exploration over candidates ×
  horizons × regimes inflates evidence; the UI states this. No ranking,
  winner selection, or formal correction is performed.
- **Provenance** — `benchmarkMeta` on the response records
  eventStudyVersion / baselineVersion / bootstrapVersion / blockLengthRule /
  replicates / seed / minimums. Stored inside the existing `signal_research`
  JSONB; pre-P11 records simply lack the field.
