# Gold Mine Lab — Backend (Phase 1: foundation)

Go service for the Gold Mine Lab quantitative research and backtesting platform.
Phase 1 provides runtime infrastructure only: config, DB pool, HTTP plumbing,
health check, consistent error format, graceful shutdown. Domain packages exist
as documented scaffolding for later phases — no strategy/backtest/metrics logic.

## Stack

- Go 1.22+ (`go 1.22` in go.mod; developed against toolchain 1.25)
- Gin (v1.10) HTTP router
- PostgreSQL via pgx/v5 + pgxpool
- `log/slog` structured logging (text in dev/test, JSON in prod)
- Config from environment variables only

## Layout

```
backend/
├── cmd/server/main.go    # thin entrypoint: config → logger → pool → router → run → shutdown
├── internal/
│   ├── api/              # router + Deps, middleware (request-ID, logging,
│   │                     #   recovery, CORS), error envelope, health handler
│   ├── config/           # env loading + fail-fast validation
│   ├── database/         # pgxpool constructor + Ping (own 2s timeout)
│   ├── demo/             # deterministic in-memory impl of the service contracts
│   ├── market/           # Asset, Timeframe, Candle, DataProvider interface
│   ├── strategy/         # Strategy, Signal, ParameterSpec, Registry interface
│   ├── backtest/         # doc.go only (later phases)
│   ├── metrics/          # doc.go only (later phases)
│   └── portfolio/        # doc.go only (later phases)
├── migrations/           # empty — no tables in Phase 1
├── tests/                # integration tests (//go:build integration)
├── docker-compose.yml    # local Postgres 16 (host port 5433)
├── Makefile              # run / build / test / vet / db-up / db-down
└── .env.example          # placeholder values only
```

## Configuration

All config comes from env vars (`os.Getenv`). `DATABASE_URL` is required — it
has no default because it carries credentials. Everything else has a safe
default. Invalid or missing values fail startup with a complete error list.

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

## Run it

```bash
cp .env.example .env          # optional; or export vars directly
docker compose up -d postgres # local Postgres (host port 5433)
go run ./cmd/server           # or: make run
```

If you have `make`: `make db-up`, `make run`, `make test`, `make db-down`.
(Windows without make: use the commands inside the Makefile directly.)

Port note: the compose file maps Postgres to host **5433** so it can coexist
with a locally installed Postgres already bound to 5432.

## API

All routes live under `/api` and emit the field names the frontend's Zod
contracts expect. Handlers stay thin — every route delegates to a service
interface (`MarketService`, `StrategyService`, `BacktestService`,
`TradeService` in `internal/api/contract.go`). Phase 1 wires them to
`internal/demo`, a deterministic in-memory implementation (seeded mulberry32,
fixed historical window ending 2024-12-31). Swap `demo.New()` in `main.go`
for Postgres-backed services in a later phase — no handler or frontend change.

| Method | Path                                | Payload                                   |
| ------ | ----------------------------------- | ----------------------------------------- |
| GET    | `/api/health`                       | `{status, service, database}`             |
| GET    | `/api/assets`                       | `[Asset]`                                 |
| GET    | `/api/market/candles?asset&timeframe` | candles + EMA20/EMA50 overlays          |
| GET    | `/api/market/summary?asset&timeframe` | market overview tile                    |
| GET    | `/api/strategies`                   | strategy + last signal + performance      |
| GET    | `/api/strategies/:id`               | one strategy (404 envelope if unknown)    |
| GET    | `/api/signals?asset&strategy&limit` | newest-first signals                      |
| GET    | `/api/backtests`                    | runs (`?strategy=` filter)                |
| GET    | `/api/backtests/latest`             | `{runId}` or `{runId: null}`              |
| GET    | `/api/backtests/summary`            | dashboard aggregate                       |
| GET    | `/api/backtests/:id`                | run + metrics + equity curve + trades     |
| POST   | `/api/backtests`                    | run request → `201` + run (validates input) |
| GET    | `/api/trades`                       | paginated trades (`strategy|side|result|from|to|page|pageSize`) |

### `GET /api/health`

- DB reachable → `200 {"status":"ok","service":"gold-mine-lab-backend","database":"up"}`
- DB unreachable → `503 {"status":"degraded","service":"gold-mine-lab-backend","database":"down"}`

The handler calls a `HealthChecker` interface — no DB code in the handler.

### Errors

All failures use one envelope:

```json
{"error": {"code": "not_found", "message": "resource not found", "request_id": "…"}}
```

404 and 405 responses use the same shape. Internal error details are logged
server-side, never leaked to clients. `X-Request-ID` is honoured inbound and
echoed on every response.

### Graceful shutdown

SIGINT/SIGTERM → stop accepting requests → drain in-flight requests within
`SHUTDOWN_TIMEOUT` → close the DB pool.

## Database

Phase 1 configures only connectivity and pooling — no tables yet. Planned
schema (later phases, via migrations):

| Table              | Purpose                                     |
| ------------------ | ------------------------------------------- |
| `assets`           | tradeable instruments (XAUUSD, MSFT, …)     |
| `strategies`       | registered strategies + parameter schemas   |
| `strategy_signals` | emitted signals                             |
| `backtest_runs`    | run configuration + status                  |
| `backtest_trades`  | per-run simulated trades                    |
| `backtest_metrics` | computed run metrics (win rate, PF, max DD) |

Recommended migration tool: **golang-migrate** (`migrate` CLI + `migrations/`
directory already reserved). Not wired in during Phase 1.

## Testing

```bash
go test ./...                          # unit tests (no DB needed)
go test -tags=integration ./tests/     # real-DB test; skips if DATABASE_URL unset
```

Covered: config validation (valid env, missing required var, invalid values),
health handler 200/503 via a fake `HealthChecker`, error envelope on 404/405,
request-ID passthrough, CORS allow/deny, panic recovery, and a graceful-
shutdown test that starts the real server and injects SIGTERM.

## Deliberately absent (Phase 1)

Real strategy evaluation, a real backtest engine, any DB schema, auth,
WebSockets, broker/live-market-data integrations. The `/api/*` data endpoints
serve deterministic synthetic data (`internal/demo`) so the frontend runs
against the real REST contract today; the `Historical / Demo` market status
keeps that honest until Postgres repositories land.
