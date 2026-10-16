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
(see `frontend/.env.example`).

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
