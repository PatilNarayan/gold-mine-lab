# Project Instructions

## What this is
Gold Mine Lab — a quantitative research workstation for XAU/USD trading strategies.
Historical backtesting, walk-forward/OOS evaluation, signal research, and shadow
(simulated) trading. **It does not execute real trades** — preserve that boundary
in all copy and UI.

## Tech Stack
- **Frontend**: React 18 + TypeScript + Vite, Tailwind CSS, React Router,
  TanStack Query, Zustand (persisted to `localStorage` key `gml.ui`), Zod,
  ECharts + Lightweight Charts, React Flow, Lucide icons.
- **Backend**: Go 1.23, Gin, PostgreSQL via pgx/v5, golang-migrate SQL migrations,
  coder/websocket.
- **Infra**: Docker Compose for Postgres (`backend/docker-compose.yml`),
  `scripts/app.ps1` orchestrates db + backend + frontend.

## Build & Run
- Everything: `.\scripts\app.ps1 start` (needs Docker Desktop for Postgres).
- Frontend only (no backend needed): `cd frontend && npm run dev` with
  `VITE_DATA_MODE=mock` — full seeded demo dataset, shows "Demo data" banner.
- Frontend checks: `npm run typecheck` `npm run lint` `npm run test` `npm run build`
- Backend: `cd backend && go build ./cmd/server` — migrations apply at startup.

## Code Style
- **Design tokens are the source of truth.** Never raw palette classes
  (`text-red-400`, `amber-*`). Use semantic tokens: `surface-*`, `line`,
  `content-*`, `accent`, `buy`, `sell`, `warning`, `info`. Tokens live in
  `frontend/src/index.css` → mapped in `tailwind.config.js` → read by chart
  theme (`src/charts/theme.ts`).
- Shared primitives in `src/components/common/`: `Button`, `LinkButton`,
  `PageHeader`, `EmptyState`, `ConfirmDialog`. Use them — don't re-add raw
  `.btn`/`.card` classes or `window.confirm`.
- Modal behavior (focus trap, Escape, scroll lock, focus restore) goes through
  `src/hooks/useModalLayer.ts`.
- Table row navigation uses `DataTable`'s `rowHref` prop (real `<Link>`s),
  never `tabIndex` + Enter/Space handlers.
- `tnum` class for tabular numerals; `.section-label` for 10px caps labels.
- Respect `prefers-reduced-motion` — CSS block is global; JS/charts must check
  `prefersReducedMotion()` from `src/charts/theme.ts`.

## Conventions
- Pages are lazy-loaded via `router.tsx`; app routes sit inside `AppShell`,
  standalone pages (e.g. `/landing`) outside it.
- Data hooks in `src/api/` (TanStack Query); all API shapes validated with Zod
  in `src/api/schemas.ts` — mirror backend JSON exactly, don't invent fields.
- Two data modes: `api` (real backend) and `mock` (seeded demo). Never blur them.
- **Honesty invariant**: label simulated/shadow fills as simulated; never claim
  live data, execution, certifications, or profitability not backed by the API.

## Testing
- `npm run test` — Vitest + Testing Library; files colocated `*.test.tsx`.
- Route smoke tests in `src/app/routes.smoke.test.tsx`.

## Project Structure
```
frontend/src/
  api/        → TanStack Query hooks + Zod schemas (mock + api impls)
  app/        → router, providers, smoke tests
  charts/     → ECharts/Lightweight Charts wrappers, theme.ts
  components/ → layout (AppShell/Header/Sidebar/MobileNav), common/, tables/
  hooks/      → useModalLayer etc.
  pages/      → Dashboard, Market, Strategies, Backtests, Results, Compare,
                WalkForward, Graphs, SignalResearch, Studies, Trades, Shadow,
                Landing
backend/
  cmd/server, cmd/datasync → entry points
  internal/    → api, service, repository, backtest, shadow, strategy,
                 strategygraph, live, market, metrics, portfolio, regime,
                 eventbench, demo, config, database
  migrations/  → numbered up/down SQL (golang-migrate)
scripts/app.ps1 → start/stop/status/restart orchestrator
```

## Do NOT modify without explicit request
- Strategy mathematics, backtest execution semantics, financial calculations,
  shadow-engine concurrency/transaction architecture, existing DB migrations
  (add new numbered files instead).
