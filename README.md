# Gold Mine Lab

A quantitative strategy research and backtesting platform — asset-agnostic
(XAUUSD, XAGUSD, MSFT, TSLA, …), starting with historical research workflows.

## Projects

| Folder      | Stack                                             | Docs                 |
| ----------- | ------------------------------------------------- | -------------------- |
| `frontend/` | React 18 + TypeScript + Vite + Tailwind           | `frontend/README.md` |
| `backend/`  | Go 1.22+ · Gin · PostgreSQL (pgx/v5)              | `backend/README.md`  |

## Quick start

```bash
# Backend — Postgres + API on :8080
cd backend
docker compose up -d postgres
cp .env.example .env            # fill in DATABASE_URL etc., then export or source it
set -a && . ./.env && set +a    # bash; or set vars however your shell prefers
go run ./cmd/server

# Frontend — dev server on :5173
cd frontend
npm install
cp .env.example .env            # set VITE_DATA_MODE=api to use the backend
npm run dev
```

Frontend connects to the backend via `VITE_API_URL` + `VITE_DATA_MODE=api`
(see `frontend/.env.example`). Or use `.\scripts\app.ps1 start` to launch
Postgres + backend + frontend together (API mode is selected automatically).

## Live market data (optional)

The app can stream real XAU/USD quotes into the Market page's **Live** mode.
Two providers are supported — select with `LIVE_MARKET_PROVIDER`:

- `twelvedata` (default) — [Twelve Data](https://twelvedata.com) via REST
  polling or WebSocket
- `tiingo` — [Tiingo](https://www.tiingo.com) Forex firehose WebSocket
  (`wss://api.tiingo.com/fx`, ticker `xauusd`). Tiingo's free plan documents
  Forex WS access; quotes are top-of-book **midpoints** (the UI tags the price
  `mid`), not last trades. Candle history seeds from
  `GET /tiingo/fx/xauusd/prices` when the account permits it.

This is strictly observational — it never feeds backtests, strategies, or
persisted research.

```bash
# backend/.env — Twelve Data
LIVE_MARKET_ENABLED=true
LIVE_MARKET_PROVIDER=twelvedata
LIVE_MARKET_TRANSPORT=rest            # rest | websocket — rest is the default
LIVE_MARKET_POLL_INTERVAL=10s         # 5s–10m; ≈6 API credits/min at 10s
TWELVE_DATA_API_KEY=<your key from https://twelvedata.com>

# backend/.env — Tiingo (websocket transport implied; no REST polling)
LIVE_MARKET_ENABLED=true
LIVE_MARKET_PROVIDER=tiingo
TIINGO_API_TOKEN=<your token from https://www.tiingo.com>
```

The Tiingo token travels in the WS subscribe-frame body and the REST
`Authorization: Token` header — never in a request URL — so it cannot leak via
logged URLs.

Endpoints: `GET /api/market/live/status` · `GET /api/market/live/quote?asset=xauusd`
· `GET /api/market/live/candles?asset=xauusd&timeframe=15m&limit=200`
· `WS /api/market/live/stream`.
Timeframes: 1m 5m 15m 1h 4h 1d. The API key stays server-side; the browser
bootstraps quote/candles over REST, then receives every provider tick over
the `/stream` WebSocket — the forming candle and its EMA20/EMA50 overlays are
recomputed per tick by the backend and pushed as incremental frames, so the
chart updates without polling or page refresh. The status response reports
the actual `transport` in use, last provider update, last successful fetch,
and an `estimatedDailyCredits` figure — there is no silent fallback between
transports.

All candle timestamps — historical and live, every timeframe — are broker
civil-time labels (Kyiv, EET/EEST) rendered as naive UTC, matching the MT5
export convention: identical labels always denote identical market
intervals. Provider-native 4h/1d rows are UTC-bucketed, so those books
aggregate the provider's 1h series into Kyiv civil buckets.

**Sample configurations**

```bash
# Limited trial / Basic account (~800 API credits/day) — run during market
# hours only; ~2,016 credits/day continuous:
LIVE_MARKET_POLL_INTERVAL=60s
LIVE_MARKET_CANDLE_RECONCILE_INTERVAL=15m

# Paid / higher-quota account (~10,368 credits/day continuous):
LIVE_MARKET_POLL_INTERVAL=10s
LIVE_MARKET_CANDLE_RECONCILE_INTERVAL=5m
```

The backend logs a warning at startup when the estimated daily usage exceeds
`LIVE_MARKET_DAILY_CREDIT_LIMIT` (default 800). Estimate =
`86400/poll + 6×86400/reconcile` upstream calls/day.

**Transports**

- `rest` (default): one managed worker polls `GET /quote` on the configured
  interval (1 API credit/call) and feeds the same quote/candle caches. Works
  on any plan that covers XAU/USD — no WebSocket entitlement needed. It is a
  periodic snapshot, not a tick stream; the UI shows the cadence and the
  provider's own timestamp.
- `websocket`: streams tick prices over `wss://ws.twelvedata.com/v1/quotes/price`
  — requires a plan with WebSocket access (full access is Pro-tier).

Notes:

- **XAUUSD is the only instrument.** Other assets are rejected with HTTP 400.
- **Plan requirements** (per Twelve Data's current pricing docs): XAU/USD is a
  *commodity* — REST `time_series` for it requires a plan covering commodities
  (**Grow+**; the free Basic tier covers only US stocks, forex, and crypto).
  Full WebSocket streaming requires **Pro** (Basic/Grow get a limited set of
  WS trial symbols only). The `demo` API key is rejected for XAU/USD on both
  REST and WebSocket. The status endpoint reports plan/auth failures
  truthfully — no fake prices.
- Without a key or with `LIVE_MARKET_ENABLED=false`, the UI shows "Live data
  is not configured" and historical mode works exactly as before.

## Historical market data

The runtime has no bundled datasets: the configured `LIVE_MARKET_PROVIDER`
(Tiingo by default) backfills the Postgres `market_candles` table on demand
via its REST history API — validated, session-labeled, provenance-stamped —
and `candle_sync` records the contiguous synced span. Bootstrap pre-warms
15m/1h/4h/1d over the provider's full range (Tiingo FX: 2020-01-01 onward);
1m/5m sync lazily on first read. Missing coverage yields honest empty
states, never generated candles.

`cmd/datasync` and `scripts/sync-xauusd-history.*` remain available as an
**optional archival path**: they produce a versioned CSV snapshot from
provider data (see below) for offline analysis — nothing in the runtime
reads them.

## Refreshing historical data (archival, optional)

`scripts/sync-xauusd-history.ps1` (or `.sh`) produces a **new versioned
snapshot** of the CSV dataset extended through the latest completed
Twelve Data candles — the original `data/` files are never modified.

```powershell
.\scripts\sync-xauusd-history.ps1                          # incremental sync → data-twelvedata-v2\
.\scripts\sync-xauusd-history.ps1 -DryRun                  # preview request count, no writes
.\scripts\sync-xauusd-history.ps1 -Out data-twelvedata-v3 -Timeframes 15m,1h -MaxRequests 20
```

- Fetches `time_series` in bounded ≤4000-bar UTC windows with request pacing
  (`-RequestInterval`, default 8s) and a hard request cap (`-MaxRequests`).
- **Convention**: the dataset is MT5 broker time (EET/EEST ≈ `Europe/Kyiv`);
  provider UTC bars are relabeled for 15m/1h and re-aggregated into Kyiv
  buckets for 4h/1d. An overlap probe must match ≥3 source rows within
  0.15% before any splice happens — otherwise the timeframe aborts.
- Validation rejects duplicates, out-of-order, and implausible OHLC; market
  closures are reported as gaps, never filled. Appended volume is `0`
  (the provider supplies none) — documented in `manifest.json`.
- `manifest.json` records source/result SHA-256 per file, date ranges,
  request count, and any discontinuities found in the source.
- Idempotent: re-running appends only newer bars. Snapshots are archival
  inputs for `CSVProvider`-based tooling and tests; every backtest records
  the exact dataset fingerprint it used, so runs stay attributable.
- `TWELVE_DATA_API_KEY` is read from the environment or `backend/.env`
  (never a CLI flag, log, or manifest field).

## Legacy CSV downloads (tests/tooling only)

The original MT5-export CSVs are still fetchable for tests, archival, and
`CSVProvider`-based tooling — they are NOT required by the runtime:

- Windows PowerShell 5.1: `.\scripts\fetch-data.ps1`
- Linux/macOS: `./scripts/fetch-data.sh`

Both scripts place verified files in the gitignored `data/` directory. By
default, all six timeframes are downloaded; PowerShell accepts
`-Timeframes 1d,1h`, while the shell script accepts
`--timeframes 1d 1h`. The release assets are packaged from the HuggingFace
mirror `ZombitX64/xauusd-gold-price-historical-data-2004-2025` of the Kaggle
MetaTrader XAUUSD export by novandraanugrah. The data covers
2004-06-11 through 2025-09-30. The expected daily fingerprint is
`xauusd:1d@sha256:5335b0dc2df9f9e980f4a6d32f70711f694eff7e461e092bb9e81e0fe619d802`.
Set `GML_DATA_BASE_URL` to use a compatible mirror or local download server.
