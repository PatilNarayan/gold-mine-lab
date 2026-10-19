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

The app can stream real XAU/USD quotes from [Twelve Data](https://twelvedata.com)
into the Market page's **Live** mode. This is strictly observational — it never
feeds backtests, strategies, or persisted research.

```bash
# backend/.env
LIVE_MARKET_ENABLED=true
TWELVE_DATA_API_KEY=<your key from https://twelvedata.com>
```

Endpoints: `GET /api/market/live/status` · `GET /api/market/live/quote?asset=xauusd`
· `GET /api/market/live/candles?asset=xauusd&timeframe=15m&limit=200`.
Timeframes: 1m 5m 15m 1h 4h 1d. The API key stays server-side; the frontend
only polls the backend's cached state.

Notes:

- **XAUUSD is the only instrument.** Other assets are rejected with HTTP 400.
- WebSocket streaming needs a Twelve Data plan that permits it (full access is
  a Pro-tier feature; lower tiers may connect but only to trial symbols). The
  status endpoint reports plan/auth failures truthfully — no fake prices.
- REST `time_series` (used to seed/reconcile the live chart) requires Basic+.
- Without a key or with `LIVE_MARKET_ENABLED=false`, the UI shows "Live data
  is not configured" and historical mode works exactly as before.

## Market data

From the repository root, fetch and verify the historical CSVs with one command:

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
