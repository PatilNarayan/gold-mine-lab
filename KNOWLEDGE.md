# Gold Mine Lab — Project Knowledge Base

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
data/XAU_*_data.csv  (real Kaggle XAUUSD history, 2004→2026)
   ↓  CSVProvider: validated streaming reads (Tail for charts, Range+warmup for backtests)
ema-cross strategy   (EMA20/EMA50 crossover — pure math, no I/O)
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
| Market candles, EMA overlays, volume | **Real** Kaggle XAUUSD CSVs | Real prices |
| BUY/SELL signal markers + signals table | **Real** — ema-cross evaluated over real candles | Real crossovers |
| Backtest trades/metrics/equity curves | **Real** — engine replayed over real candles | Real simulated results (a real backtest, not fabricated) |
| Strategy list / performance tiles | Real — aggregated from persisted runs | Real |
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

## Database schema

`assets`, `strategies` (slugs, `supported_timeframes` jsonb), `strategy_signals`,
`backtest_runs` (+`equity_curve` jsonb, `engine_version`, `strategy_version`,
`dataset`), `backtest_trades` (+`gross_pnl`, `entry_commission`,
`exit_commission`), `backtest_metrics` — see `migrations/000001` and
`000002_accounting_consistency`. Indexes per access pattern:
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
- Market data zip stays in `~/Downloads`; extracted CSVs live in `data/`
  (gitignored — never commit the 470 MB).
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
