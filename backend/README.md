# Gold Mine Lab — Backend

Go service for the Gold Mine Lab quantitative research and backtesting platform.
Phase 2 replaced the demo layer with the real research pipeline:

```
Market provider (Tiingo / Twelve Data REST history)
      ↓  market.DBProvider             # sync-on-read into Postgres, validated,
      ↓  market_candles + candle_sync  # provenance + contiguous coverage
Strategy Engine (strategy.Registry)    # ema-cross + rsi-mean-reversion v1.0.0, pure compute
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
│   ├── market/               # Asset/Timeframe/Candle + HistoricalSource iface
│   │   ├── db.go             # DBProvider: Postgres store + provider sync-on-read
│   │   └── csv.go            # CSVProvider: test fixtures + legacy demo only
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
| `LIVE_MARKET_ENABLED`   | `false`                          | enable live XAU/USD feed     |
| `LIVE_MARKET_PROVIDER`  | `twelvedata`                     | `twelvedata` \| `tiingo`     |
| `TWELVE_DATA_API_KEY`   | —                                | secret; required for provider `twelvedata`, never logged/served |
| `TIINGO_API_TOKEN`      | —                                | secret; required for provider `tiingo`, never logged/served |
| `LIVE_MARKET_SYMBOL`    | `XAU/USD`                        | Twelve Data symbol for xauusd |
| `LIVE_MARKET_STALE_AFTER` | `30s`                          | quote age → `stale` state    |
| `LIVE_MARKET_TRANSPORT` | `rest` (`websocket` for tiingo)  | `rest` polls /quote; `websocket` streams ticks; `rest`+`tiingo` is rejected |
| `LIVE_MARKET_POLL_INTERVAL` | `10s`                      | REST quote cadence (5s–10m); ≈8,640 credits/day at 10s |
| `LIVE_MARKET_CANDLE_RECONCILE_INTERVAL` | `5m`       | per-timeframe REST refresh (30s–1h); ≈1,728 credits/day at 5m |
| `LIVE_MARKET_DAILY_CREDIT_LIMIT` | `800`                  | plan allowance for the startup usage warning |
| `TWELVE_DATA_REST_URL`  | `https://api.twelvedata.com`     | override for tests/mocks     |
| `TWELVE_DATA_WS_URL`    | `wss://ws.twelvedata.com/v1/quotes/price` | override for tests/mocks |
| `TIINGO_WS_URL`         | `wss://api.tiingo.com/fx`          | override for tests/mocks     |
| `TIINGO_REST_URL`       | `https://api.tiingo.com`           | override for tests/mocks     |

## Live market data (`internal/live`)

`LIVE_MARKET_PROVIDER` selects the upstream source — `twelvedata` (default) or
`tiingo`. Both feed the same quote cache, candle books, and status contract;
there is no silent fallback between them.

**Twelve Data** supports two transports:

- `rest` (default, `LIVE_MARKET_TRANSPORT=rest`): one managed worker polls
  `GET /quote` every `LIVE_MARKET_POLL_INTERVAL` (1 credit/call — 10s ≈
  6 credits/min). No WebSocket entitlement needed. Failures back off
  exponentially; auth rejections park in `config_error`.
- `websocket` (`LIVE_MARKET_TRANSPORT=websocket`): one WS subscription
  streams tick prices (requires a plan with WS access). Reconnects with
  1s→60s backoff + jitter; refused handshakes escalate to `config_error`.

REST `/time_series` seeds/reconciles candle books on a bounded schedule in
both modes (≥30s between fetches, ≥60s cache TTL per timeframe). Quotes are
validated (finite positive price, plausible+monotonic provider timestamp);
missing provider timestamps stay null rather than being invented; bid/ask/
volume pass through only when supplied. States:
`disabled|config_error|connecting|connected|stale|disconnected`, plus the
actual `transport` in the status response.

**Tiingo** (`LIVE_MARKET_PROVIDER=tiingo`, websocket-only): one subscription
to `wss://api.tiingo.com/fx` streams FX top-of-book frames for `xauusd`. The
token is sent in the subscribe-frame `authorization` field and the REST
`Authorization: Token` header — never in a URL. Quote updates carry bid/ask
and a midpoint; the surfaced `price` is that midpoint and is tagged
`priceKind="mid"` end-to-end so the UI never labels it a last trade. Candle
books seed from `GET /tiingo/fx/{ticker}/prices?resampleFreq=…` when the
account permits it; otherwise forming candles build from genuine stream
updates only. The server heartbeat (~30s) is used as a liveness signal —
90s of silence forces a reconnect. Same reconnect/config_error lifecycle as
Twelve Data.

Live data is strictly observational — streamed quotes never enter the
historical store, the engine, strategies, or the database. `xauusd` is the
only served asset; anything else gets HTTP 400.

## Historical market data (`internal/market`)

There are no bundled CSV datasets at runtime. The configured
`LIVE_MARKET_PROVIDER` also supplies history: `DBProvider` serves candles
from the Postgres `market_candles` table and, on a coverage miss, pages the
provider's REST history API (`FetchHistory`), sessionizes the rows into the
canonical Kyiv-label convention, validates OHLC invariants, upserts with
provider provenance, and records the contiguous synced span in
`candle_sync`. A request outside synced+provider coverage returns whatever
genuinely exists — empty included — never fabricated bars; provider failures
propagate or degrade to stored data, never to mock data. Coverage written by
one provider is never extended by another (no silent source mixing).
Bootstrap pre-warms 15m/1h/4h/1d over the provider's full range; 1m/5m sync
lazily on first read. Tiingo FX history starts 2020-01-01 and REST burst
rate-limits apply — coverage accumulates incrementally and retries on the
next read. With `LIVE_MARKET_ENABLED=false` the store is read-only and
serves previously synced candles only.

**Timestamp contract**: `Candle.Time` is a *session label* — broker civil
time (Europe/Kyiv, EET/EEST) rendered as a naive UTC timestamp — on every
timeframe, identical to what the CSV provider produces for the same market
interval. Kyiv's offset is always a whole number of hours, so provider
1m/5m/15m/1h rows are relabeled 1:1; provider-native 4h/1d rows are
UTC-bucketed and structurally incompatible, so those books seed from the
provider's 1h series and aggregate into Kyiv civil buckets. Identical labels
therefore always mean identical market intervals across historical and live
data — bootstrap and stream merge into one forming candle per interval. A
quote's `providerTimestamp`, by contrast, is a real UTC instant; the panel
renders it in Kyiv civil time so it shares the chart's clock. During the
autumn fall-back two real hours share one label — the same ambiguity the
broker export encodes.

**Browser delivery**: `GET /api/market/live/stream` upgrades to WebSocket and
pushes normalized frames — `status` (on transitions), `quote` (per provider
tick), and `bars` (the newest bar + EMA20/EMA50 point per timeframe, computed
over the same closed+forming window REST returns). One upstream provider
subscription feeds every connected browser via an internal fan-out hub;
per-client buffers are bounded and frames are snapshots, so a dropped frame
self-heals on the next tick. The frontend bootstraps via REST, then applies
stream frames to the same query cache — no browser refresh or REST polling
drives the chart.

## Run it

```bash
docker compose up -d postgres   # Postgres on host port 5433 (5432 often taken)
export DATABASE_URL="postgres://goldmine:goldmine@localhost:5433/goldmine?sslmode=disable"
export LIVE_MARKET_ENABLED=true LIVE_MARKET_PROVIDER=tiingo TIINGO_API_TOKEN=…
go run ./cmd/server               # migrates, seeds, then serves :8080
```

Startup sequence: `migrations.Up` → pgx pool → live provider →
`DBProvider` (history syncs into `market_candles` on demand) → registry
(ema-cross) → service → **Bootstrap** (idempotent: upsert asset + strategy
defs, eager candle sync for coarse timeframes, generate real signals from
the stored tail, run 3 seed backtests on an empty DB) → serve.

## Legacy CSV format (tests/datasync only)

Files: `<PREFIX>_<tf>_data.csv` where PREFIX = symbol minus quote currency
(`XAUUSD` → `XAU_`). Header `Date;Open;High;Low;Close;Volume`,
semicolon-delimited, timestamps `YYYY.MM.DD HH:MM` as Kyiv civil labels.
`CSVProvider` is retained for deterministic tests, `cmd/datasync` archival
exports, and the `internal/demo` fixture — it is never wired into the
runtime pipeline.

`CSVProvider`:
- `Tail(asset, tf, n, asOf)` — last n candles ≤ asOf; zero asOf = the
  dataset's actual end (market endpoints report real freshness, no simulated
  "now")
- `Range(asset, tf, from, toExclusive, warmup)` — every candle in
  `[from, toExclusive)` plus up to `warmup` bars before `from` for indicator
  warm-up; streams the file so large ranges never OOM; aborts on the first
  bad row
- Date convention: `startDate`/`endDate` are inclusive at date granularity;
  the service converts them to `[start, endDate+1day)` — a candle at next
  day's midnight is excluded.
- One shared `scanRows` validator for charts and backtests: explicit header
  recognition (`Date;Open;High;Low;Close;Volume`), `file:line` errors,
  finite positive OHLC (`NaN`/`±Inf` rejected), `high ≥ max(o,c)`,
  `low ≤ min(o,c)`, `high ≥ low`, volume ≥ 0, strictly ascending, no
  duplicate timestamps — bad rows are rejected, never silently dropped.

`GetCandles`/`Tail` and `Range` cannot disagree about data integrity — they
share the same scan policy.

## Strategy: ema-cross v1.0.0

Parameters: `fast_period` (20), `slow_period` (50); validated `0 < fast < slow`.

Rules, evaluated at each bar's close (EMA seeded at first close, k=2/(n+1)):

- **BUY**  — `prevFast ≤ prevSlow` AND `fast > slow`
- **SELL** — `prevFast ≥ prevSlow` AND `fast < slow`
- **NEUTRAL** otherwise — a cross must actually occur
- first `slow_period` bars are warm-up and can only emit NEUTRAL
- score = `min(1, |fast−slow|/price·20)`; reason e.g. `"EMA20 crossed above EMA50"`

`strategy.Stream` (optional interface) gives O(1)-per-bar evaluation; the
engine falls back to prefix `Evaluate` otherwise. Both paths are proven
equivalent bar-for-bar by `TestEMACrossStreamEqualsEvaluate`.

## Strategy: rsi-mean-reversion v1.0.0

Parameters: `rsi_period` (14, int ∈ [2,100]), `oversold` (30, [1,49]),
`overbought` (70, [51,99]); validated, `oversold < overbought`.

Wilder RSI: seeded by the simple average of the first `period` changes, then
`avg = (prevAvg·(period−1) + x)/period`. Defined from index `period`;
`avgLoss=0` → RSI 100 (avgGain>0) or 50 on constant prices.

Rules (long-only):

- **BUY**  — `prevRSI ≤ oversold` AND `RSI > oversold` (upward cross)
- **SELL** — `prevRSI ≥ overbought` AND `RSI < overbought` (downward cross —
  exits the long; never shorts)
- **NEUTRAL** otherwise — crossing is an event; remaining beyond the
  threshold does not re-signal
- First `period+1` bars are warm-up → NEUTRAL; `WarmupBars() = 3·period+2`
- Metadata carries rsi/prev_rsi/thresholds/period; reason e.g.
  `"RSI14 crossed above oversold 30"`
- These rules are a research hypothesis, not a claimed edge

Same engine, same fill model, same accounting — equivalence proven by
`TestRSIStreamEqualsEvaluate`.

## Comparisons (Phase 4)

`comparisons` table groups member runs (`backtest_runs.comparison_id`,
`sample` ∈ in_sample|out_of_sample). `ExecuteComparison` validates ≥2
distinct legs, enforces `oosStart > endDate`, then executes each leg through
the normal `Execute` path — identical capital/sizing/costs/dataset/engine,
per-leg parameters only. Status: completed | partial_failed | failed.
Out-of-sample legs use a strictly later window; warm-up may extend before it
but trades/metrics belong to the leg window only.

Timeframe is a **per-run choice**: `Definition.timeframe` is the default and
`supportedTimeframes` (15m/1h/4h/1d) bounds what a run may request —
validation rejects anything else. EMA warm-up is parameter-derived:
`Warmupper.WarmupBars() = 3× slow_period` pre-window bars for convergence.

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
6. Accounting (engine 2.1): `grossPnL = (exitFill − entryFill)·qty`;
   `netPnL = grossPnL − entryComm − exitComm`. Trade `pnl` is **net**;
   gross + both commissions are stored separately. Win/loss classified on
   net — a gross-positive trade can be a net loss.
7. Equity is mark-to-market at each bar's close. On the final bar the open
   position is liquidated at `close·(1−s%)` inside the curve — the terminal
   equity point equals `initial + Σ netPnL` exactly.
8. A run is persisted atomically: trades + metrics + equity curve + status
   `completed` in one transaction; failures write status `failed` + a safe
   message. Never a partially-completed run.

## Shadow simulation (`internal/shadow`, `internal/service/shadowdriver.go`)

**Simulation only — never broker orders.** A persistent shadow engine replays
the frozen EMA20/50 benchmark against the live quote stream.

**Two coordinate systems (do not mix):** `bar_time`/`signal_bar_time` are
*labels* — Kyiv civil time rendered as naive UTC (`live.sessionBucket`).
Quote times, `received_at`, `decision_at`, and execution windows are *real
instants*. Convert with `live.LabelToInstant(label)` /
`live.NowLabel()`; never compare `time.Now()` directly to a `bar_time`.

- **Decisions** (`shadow_decisions`): append-only ledger — one row per
  evaluated close with source (`live`|`recovery`), action, reason, EMA
  values, actionability and order link. First write wins
  (`ON CONFLICT DO NOTHING`): the original evaluation is immutable.
  `CHECK (actionable = false OR order_id IS NOT NULL)` enforces the
  decision→order link.
- **Orders**: BUY at ask, SELL at bid, mid never executes. Pending orders
  fill only on quotes with provider_time ≥ `decision_at` and inside the
  fill timeout; otherwise `expired`. Recovery replays only persisted
  admissible quotes inside the original real-time window — never a
  current quote for an old signal.
- **missed vs unobservable**: a passed window resolves `missed` only when
  `CoverageComplete` proves the execution window was fully observed
  (recorder span + heartbeat continuity + no in-window persistence
  failure + no degraded feed interval overlapping); otherwise
  `unobservable`. The decision *anchor* is the bar close as a real
  instant (`live.LabelToInstant(bar_time) + timeframe`) — recovery
  replay searches persisted quote evidence in that real window only.
- **Sessions** (`shadow_sessions`): one open session per
  (strategy,version,symbol,tf,provider); a different effective-config
  fingerprint retires the old session (records kept) and opens a new one.
  Positions (PK `session_id`+identity), pending orders, decisions
  (`UNIQUE NULLS NOT DISTINCT` on session+identity+bar), and order keys
  (key includes `session_id`) are session-scoped — **no state is
  inherited across sessions; the session is resolved before any state
  is restored**.
- **Retry semantics**: a failed bar commit leaves the durable checkpoint
  unadvanced and keeps the bar's evaluated signal cached — the stream is
  never double-advanced. In-process continuation after a failed commit is
  NOT attempted: Postgres rollback does not roll back an advanced
  indicator stream, so the runtime halts (see *Runtime*).
- **API**: `GET /api/shadow/status`, `/api/shadow/orders`,
  `/api/shadow/decisions`.

### Shadow runtime (single-writer architecture)

`internal/shadow/runtime.go` owns the engine. Exactly ONE goroutine (the
runtime's owner) mutates `shadow.Engine`; producers only submit events:

- Provider quote callbacks → `Submit(EvQuote)` — non-blocking; the
  upstream delivery path is never stalled.
- Driver poll loop → `Submit(EvBarClosed)` (newest confirmed close) and
  `Submit(EvTick)` (expiry sweep).
- `Start` runs the bootstrap (session → restore → warmup/recovery) ON the
  owner goroutine before any live event — recovery can never interleave
  with live processing.
- HTTP handlers read only `Runtime.Snapshot()` — an immutable deep copy
  published after every event — never the engine.

**Durability — what is atomic:** `CommitBar` persists one bar's complete
transition in a single transaction: order intent (order_key dedupe) →
fill/terminal resolution → ledger decision (session+bar dedupe) →
position/`last_bar_time` checkpoint. `FillOrderAndPosition` (live fills)
and `SetOrderStatus` (expiry) stay separate atomic units.

**Failure semantics — what requires restart:** any persistence error marks
the runtime `unhealthy` (state + reason in `/api/shadow/status` →
`runtime`) and HALTS all strategy/execution processing. Recovery is a
process restart, which rebuilds from committed state — idempotent keys
(order_key, decision unique key, session-scoped position PK) make replay
safe, and the atomic commit means an ambiguous commit resolves by
inspecting durable state: either the whole transition landed or none did.

**Mailbox overload:** bounded (1024). Quote/bar events that can't be
admitted are REJECTED (`ErrMailboxFull`) and degrade the runtime — a
dropped observation can never masquerade as complete coverage. Tick
overflow only rejects (one deferrable sweep).

**Ownership:** `pg_try_advisory_lock` on a dedicated pooled connection
(`AcquireShadowOwnership`, key = FNV of the runtime identity). A second
process can't co-own the session; losing the connection degrades the
runtime; the lease is released on orderly shutdown.

**Shutdown order:** cancel producers → join driver → `Runtime.Stop`
(drain admitted work; deadline expiry is reported, remaining events
counted dropped — never claimed durable) → recorder drain → provider
close → pool close LAST.

**Not guaranteed:** exactly-once execution across crashes (we guarantee
atomicity + safe replay instead), in-process recovery from persistence
failures, and multi-runtime orchestration.

## Metric formulas

`netPnl = finalEquity − initialCapital` · `returnPct = netPnl/initialCapital` ·
`winRate = wins/closedTrades` (classified on **net** P&L) ·
`avgTradePnl = netPnl/closedTrades` ·
`grossProfit/grossLoss = Σ` of winning/losing trade **net** P&L ·
`profitFactor = grossProfit/grossLoss` (0 when no losses — not infinity) ·
`maxDrawdown = max((peak−equity)/peak)` over the equity curve (includes the
post-liquidation terminal point).
Sharpe is deliberately absent: no defined return frequency → not faked.

## Database schema (migrations 000001 + 000002)

| Table              | Key columns                                                        |
| ------------------ | ------------------------------------------------------------------ |
| `assets`           | id(text pk), symbol, name, asset_class, currency, metadata jsonb   |
| `strategies`       | id(text pk), name, version, supported_assets jsonb, **supported_timeframes jsonb**, parameters jsonb |
| `strategy_signals` | uuid pk, strategy_id→strategies, asset_id→assets, ts, action, score, price, reason — UNIQUE(strategy_id, asset_id, timestamp) |
| `backtest_runs`    | uuid pk, strategy/asset refs, tf, dates, capital, size, commission, slippage, parameters jsonb, status, equity_curve jsonb, error, **engine_version, strategy_version, dataset** |
| `backtest_trades`  | uuid pk, run_id→runs (cascade), side, entry/exit time+price, qty, **gross_pnl**, pnl (net), **entry/exit_commission**, status, result |
| `backtest_metrics` | uuid pk, run_id→runs (cascade), metric_name, metric_value — UNIQUE(run_id, metric_name) |

Migration 000002 preserves existing rows: pre-2.1 runs are marked
`engine_version='legacy'` — their `pnl` was stored gross and they lack cost
columns. They are preserved as-is, not recomputed.

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
| GET    | `/api/market/summary` | real last candle close / change (status `Historical`) |
| GET    | `/api/market/live/status` | live provider state + last quote/error |
| GET    | `/api/market/live/quote?asset` | latest provider quote (bid/ask when supplied) |
| GET    | `/api/market/live/candles?asset&timeframe&limit` | provider-backed live candles |
| GET    | `/api/strategies` | defs + latest signal + latest-run metrics per strategy |
| POST   | `/api/comparisons` | run 2–4 strategies under identical assumptions; optional OOS window |
| GET    | `/api/comparisons` | saved comparisons (newest first) |
| GET    | `/api/comparisons/:id` | config + per-leg runs, metrics, equity curves |
| GET    | `/api/signals` | persisted crossover signals |
| GET    | `/api/backtests?strategy` | all runs |
| GET    | `/api/backtests/latest` | newest completed run ID |
| GET    | `/api/backtests/summary` | experiment count + **latest run's** metrics and equity curve |
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

- Two strategies (ema-cross, rsi-mean-reversion), one asset (XAUUSD);
  registry is static in-memory.
- Run `dataset` = `asset:tf@sha256:<16>` — content fingerprint of the exact
  CSV file used, cached per process; legacy rows keep `unknown`.
- POST /api/backtests is synchronous — large 1m windows take seconds.
- Market endpoints serve a bounded tail; full range available via Range().
- Legacy (pre-2.1) runs keep gross-P&L trades and no cost decomposition —
  they're labeled `engine_version='legacy'`, not retroactively corrected.
- Dashboard shows the latest run only; no portfolio view exists yet.
- Sharpe/benchmark metrics deferred until return frequency is defined.
- Live market data is observational only (XAU/USD via Twelve Data when
  configured); it never feeds research or execution paths.
- No auth, no websockets, no async job queue — by design for now.
