# Gold Mine Lab — Backend

Go service for the Gold Mine Lab quantitative research and backtesting platform.
Phase 2 replaced the demo layer with the real research pipeline:

```
Historical CSV (MARKET_DATA_DIR)
      ↓  market.CSVProvider            # streaming, validated, bounded
Strategy Engine (strategy.Registry)    # ema-cross v1.0.0, pure compute
      ↓  indicators.EMA
Signal Generation → strategy_signals   # real crossovers, persisted
      ↓  backtest.Engine               # next-candle-open fills, deterministic
Performance Metrics (internal/metrics)
      ↓  repository/postgres           # parameterized SQL only
PostgreSQL (golang-migrate schema)
      ↓  internal/api                  # thin handlers, envelope errors
REST API → React frontend (unchanged contract)
```

## Stack

- Go 1.23 (`go` directive; toolchain 1.25) — Gin v1.10, pgx/v5 + pgxpool,
  golang-migrate v4, `log/slog` (text dev / JSON prod)
- Config from environment variables only

## Layout

```
backend/
├── cmd/server/main.go        # thin wiring: config → migrate → pool →
│                             #   repos → registry → service → bootstrap → serve
├── migrations/               # golang-migrate source (embedded)
│   ├── 000001_initial_schema.up.sql / .down.sql
│   └── migrations.go         # migrations.Up(dbURL)
├── internal/
│   ├── api/                  # router, middleware, error envelope, handlers,
│   │                         #   contract.go = wire DTOs + service interfaces
│   ├── config/               # env loading + fail-fast validation
│   ├── database/             # pgxpool + Ping
│   ├── market/               # Asset/Timeframe/Candle + DataProvider iface
│   │   └── csv.go            # CSVProvider: Tail() + Range() + row validation
│   ├── strategy/
│   │   ├── strategy.go       # Strategy, Signal, Registry contracts
│   │   ├── registry.go       # static in-memory registry (dup IDs fail)
│   │   ├── emacross.go       # ema-cross v1.0.0 (+ Streamer for O(1)/bar)
│   │   └── indicators/ema.go # EMA + EMAState (streaming)
│   ├── backtest/engine.go    # deterministic engine — see execution model
│   ├── metrics/metrics.go    # metric formulas — see below
│   ├── repository/           # repository interfaces + row types (no logic)
│   │   └── postgres/         # pgx implementations (Store)
│   ├── service/              # api.*Service impls over repos+engine; bootstrap
│   ├── demo/                 # legacy deterministic service — tests only
│   └── portfolio/            # scaffolding
└── tests/                    # //go:build integration — real DB pipeline test
```

## Configuration

| Var                     | Default                          | Notes                        |
| ----------------------- | -------------------------------- | ---------------------------- |
| `APP_ENV`               | `dev`                            | dev \| prod \| test          |
| `HTTP_PORT`             | `8080`                           |                              |
| `LOG_LEVEL`             | `info`                           | debug \| info \| warn \| error |
| `DATABASE_URL`          | — **required**                   | postgres://…                 |
| `DB_MAX_CONNS`          | `10`                             |                              |
| `DB_MIN_CONNS`          | `2`                              | must be ≤ max                |
| `HTTP_READ_TIMEOUT`     | `10s`                            | Go duration                  |
| `HTTP_WRITE_TIMEOUT`    | `10s`                            | Go duration                  |
| `SHUTDOWN_TIMEOUT`      | `15s`                            | drain window on SIGINT/TERM  |
| `CORS_ALLOWED_ORIGINS`  | `http://localhost:5173`          | comma-separated              |
| `MARKET_DATA_DIR`       | — **required**                   | dir of historical CSVs       |

## Run it

```bash
docker compose up -d postgres   # Postgres on host port 5433 (5432 often taken)
export DATABASE_URL="postgres://goldmine:goldmine@localhost:5433/goldmine?sslmode=disable"
export MARKET_DATA_DIR="../data"   # directory holding XAU_*.csv files
go run ./cmd/server               # migrates, seeds, then serves :8080
```

Startup sequence: `migrations.Up` → pgx pool → CSV provider → registry
(ema-cross) → service → **Bootstrap** (idempotent: upsert asset + strategy
defs, generate real signals from the dataset tail, run 3 seed backtests on an
empty DB) → serve.

## Historical CSV format

Files: `<PREFIX>_<tf>_data.csv` where PREFIX = symbol minus quote currency
(`XAUUSD` → `XAU_`). Header `Date;Open;High;Low;Close;Volume`,
semicolon-delimited, timestamps `YYYY.MM.DD HH:MM` treated as UTC.
Timeframes: `1m 5m 15m 1h 4h 1d` map to files directly — no resampling.

`CSVProvider`:
- `Tail(asset, tf, n, asOf)` — last n candles ≤ asOf (market endpoints)
- `Range(asset, tf, from, to, warmup)` — every candle in the window plus up to
  `warmup` bars before `from` for indicator warm-up; streams the file so large
  ranges never OOM; aborts on the first bad row
- Validation: positive OHLC, volume ≥ 0, `high ≥ max(o,c)`, `low ≤ min(o,c)`,
  `high ≥ low`, strict ascending order, no duplicate timestamps. Errors carry
  `file:line` context — bad rows are rejected, never silently dropped.

Market endpoints truncate at `simulatedNow = 2025-10-14` (one year back for
testing — the dataset has a gap 2025-09-12 → 2025-10-15, so the latest served
candle is 2025-09-12).

## Strategy: ema-cross v1.0.0

Parameters: `fast_period` (20), `slow_period` (50); validated `0 < fast < slow`.

Rules, evaluated at each bar's close (EMA seeded at first close, k=2/(n+1)):

- **BUY**  — `prevFast ≤ prevSlow` AND `fast > slow`
- **SELL** — `prevFast ≥ prevSlow` AND `fast < slow`
- **NEUTRAL** otherwise — a cross must actually occur
- first `slow_period` bars are warm-up and can only emit NEUTRAL
- score = `min(1, |fast−slow|/price·20)`; reason e.g. `"EMA20 crossed above EMA50"`

`strategy.Stream` (optional interface) gives O(1)-per-bar evaluation; the
engine falls back to prefix `Evaluate` otherwise.

## Backtest execution model

1. Signals are generated at candle **close** on bar i.
2. Fills execute at the **next candle's open** (bar i+1) — never the same
   candle; no look-ahead (strategy only sees bars ≤ i).
3. Single long position: BUY opens when flat, SELL closes when long, anything
   else is ignored. No shorts, leverage, pyramiding, partial exits, trailing
   stops.
4. Slippage is adverse: entry `open·(1+s%)`, exit `open·(1−s%)`.
   Commission is `%` of notional charged per fill (entry and exit).
5. `positionSize` = % of current equity deployed (`qty = equity·s%/fill`).
6. Equity is mark-to-market at each bar's close inside the window; an open
   position at the last bar is liquidated at its close.
7. A run is persisted atomically: trades + metrics + equity curve + status
   `completed` in one transaction; failures write status `failed` + a safe
   message. Never a partially-completed run.

## Metric formulas

`netPnl = finalEquity − initialCapital` · `returnPct = netPnl/initialCapital` ·
`winRate = wins/closedTrades` · `avgTradePnl = netPnl/closedTrades` ·
`grossProfit/grossLoss = Σ` of winning/losing trade P&L ·
`profitFactor = grossProfit/grossLoss` (0 when no losses — not infinity) ·
`maxDrawdown = max((peak−equity)/peak)` over the equity curve.
Sharpe is deliberately absent: no defined return frequency → not faked.

## Database schema (migration 000001)

| Table              | Key columns                                                        |
| ------------------ | ------------------------------------------------------------------ |
| `assets`           | id(text pk), symbol, name, asset_class, currency, metadata jsonb   |
| `strategies`       | id(text pk), name, version, supported_assets jsonb, parameters jsonb |
| `strategy_signals` | uuid pk, strategy_id→strategies, asset_id→assets, ts, action, score, price, reason — UNIQUE(strategy_id, asset_id, timestamp) |
| `backtest_runs`    | uuid pk, strategy/asset refs, tf, dates, capital, size, commission, slippage, parameters jsonb, status, equity_curve jsonb, error |
| `backtest_trades`  | uuid pk, run_id→runs (cascade), side, entry/exit time+price, qty, pnl, status, result |
| `backtest_metrics` | uuid pk, run_id→runs (cascade), metric_name, metric_value — UNIQUE(run_id, metric_name) |

Indexes: `signals(strategy_id, asset_id, ts)`, `runs(strategy_id, asset_id,
created_at)`, `trades(run_id)`, `trades(asset_id, entry_time)`,
`metrics(run_id)`.

Migrations apply at startup via embedded golang-migrate. Manual control:
`migrate -source file://migrations -database "$DATABASE_URL" up|down|version`.

## API

Same contract as before — thin handlers → `service` → repos/engine:

| Method | Path | Notes |
| ------ | ---- | ----- |
| GET    | `/api/health` | liveness + DB check |
| GET    | `/api/assets` | from `assets` table |
| GET    | `/api/market/candles?asset&timeframe` | real CSV candles + EMA overlays |
| GET    | `/api/market/summary` | real last close / change |
| GET    | `/api/strategies` | defs + latest signal + real run aggregates |
| GET    | `/api/signals` | persisted crossover signals |
| GET    | `/api/backtests?strategy` | all runs |
| GET    | `/api/backtests/latest` | newest completed run ID |
| GET    | `/api/backtests/summary` | aggregate metrics + trade-P&L equity curve |
| GET    | `/api/backtests/:id` | run + metrics + equity + trades |
| POST   | `/api/backtests` | **executes the real engine**, persists, 201 |
| GET    | `/api/trades` | filtered paginated trades from DB |

Errors: `{"error":{"code","message","request_id"}}` everywhere; internals are
logged, never leaked.

## Testing

```bash
go test ./...                    # unit tests — no DB needed
DATABASE_URL=… MARKET_DATA_DIR=… go test -tags=integration ./...  # real pipeline
```

Covered: EMA vs known values + streaming parity, crossover rules & warm-up,
registry (dup IDs), CSV parse/validation/range/warmup/dupe/unsorted rejection,
engine next-candle fills / commission / slippage / force-close / single
position / **no look-ahead** / input validation, all metric formulas, and an
integration test running bootstrap → Execute → persisted detail over real
Postgres + real CSVs.

## Observability

Structured logs for `backtest started/completed/failed`, `bootstrap`, signal
generation, market-data config, and DB errors — each carrying `run_id`,
`strategy_id`, `asset_id`. No secrets are logged (`DATABASE_URL` never printed).

## Known limitations

- One strategy (ema-cross), one asset (XAUUSD); registry is static in-memory.
- POST /api/backtests is synchronous — large 1m windows take seconds.
- Market endpoints serve a bounded tail; full range available via Range().
- Sharpe/benchmark metrics deferred until return frequency is defined.
- No auth, no websockets, no async job queue — by design for now.
