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
(see `frontend/.env.example`). Market data CSVs are kept outside the repo
(e.g. `~/Downloads`) — never committed.
