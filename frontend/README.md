# Gold Mine Lab — Frontend (Phase 1)

A quantitative strategy research and backtesting terminal. Phase 1 runs entirely
on **deterministic mock data**; the UI is wired to service interfaces so it can
switch to the Go REST API + WebSocket backend by changing configuration, not UI
code.

## Stack & pinned majors

| Concern        | Choice                                        | Major |
| -------------- | --------------------------------------------- | ----- |
| UI             | React + TypeScript (`strict`)                 | 18    |
| Bundler        | Vite                                          | 5     |
| Styling        | Tailwind CSS (v3 — design tokens in config)   | 3     |
| Routing        | React Router (data router, `createBrowserRouter`) | 6 |
| UI state       | Zustand (selection, theme, drafts only)       | 5     |
| Server data    | TanStack Query (loading/error/cache/refetch)  | 5     |
| Candlesticks   | TradingView Lightweight Charts (v5 API: `addSeries`/`createSeriesMarkers`) | 5 |
| Other charts   | ECharts (equity, drawdown) via thin wrapper   | 5     |
| Validation     | Zod (forms + service response boundary)       | 3     |
| Tests          | Vitest + React Testing Library                | 2     |

## Run it

```bash
npm install
cp .env.example .env        # optional — defaults already point at localhost:8080
npm run dev                 # http://localhost:5173
```

Other scripts: `npm run build`, `npm run typecheck`, `npm run lint`, `npm test`.

## Configuration

```dotenv
VITE_API_URL=http://localhost:8080   # Go REST API base URL
VITE_WS_URL=ws://localhost:8080/ws   # reserved — live stream derives its URL from VITE_API_URL
VITE_DATA_MODE=mock                  # mock | api
```

`VITE_*` vars are public — never put secrets here. API/WS URLs can also be
overridden in **Settings** (persisted to localStorage; non-sensitive only).

### Data modes

- `api` (default): HTTP implementations call the backend endpoints
  (`/api/assets`, `/api/market/candles`, `/api/strategies`, `/api/signals`,
  `/api/backtests`, `/api/trades`, `/api/shadow/*`, `/api/health`). The header
  shows **API connected / unreachable** from polling `GET /api/health`.
- `mock` (opt-in via `VITE_DATA_MODE=mock`): deterministic seeded data for
  development/tests. Every screen shows a **Demo data** banner; market status
  reads "Historical / Demo".

### Mock scenarios (dev)

Append a query param to force UI states:

- `?mock=error` — every service call fails with a normalized `ApiError`
- `?mock=empty` — empty collections/series for empty states
- `?mock=slow` — ~2.5s latency to show skeletons

## Architecture

```
src/
├── app/            router (lazy routes), QueryClient
├── components/
│   ├── layout/     AppShell, Sidebar, Header, MobileNav
│   ├── cards/      StatCard, MetricCard
│   ├── charts/     CandlestickChart (lightweight-charts), EquityCurveChart,
│   │               DrawdownChart, EChart wrapper
│   ├── tables/     DataTable (loading/empty/error/sort), SignalsTable, TradesTable
│   ├── forms/      BacktestForm (params rendered from strategy schema)
│   └── common/     Badge, SignalBadge, StatusDot, Skeleton, EmptyState,
│                   ErrorState, DemoBanner, Button, Input/Select, Field
├── pages/          Dashboard, Market, Strategies, StrategyDetail, Backtests,
│                   Results, ResultsRedirect, Trades, Settings, NotFound
├── services/
│   ├── interfaces.ts     MarketService, StrategyService, BacktestService,
│   │                     TradeService, HealthService
│   ├── api/              apiClient (timeout + error normalization + request-id)
│   │                     + http* implementations against planned endpoints
│   ├── mock/             mock* implementations (latency + scenario switches)
│   ├── websocket/        RealtimeClient contract + NoopRealtimeClient
│   └── index.ts          createServices(mode) factory ← VITE_DATA_MODE
├── stores/         Zustand UI stores (asset, timeframe, ui, settings, draft)
├── hooks/          TanStack Query hooks (useCandles, useStrategies, ...)
├── types/          Zod schemas + inferred domain types mirroring the backend
├── mock/           Seeded deterministic generators (candles/EMAs, signals,
│                   strategies, runs, trades, equity)
├── charts/         Shared chart theme read from CSS design tokens
└── lib/            formatters (Intl), constants, config, trade filters,
                    backtest form validation
```

### Key rules enforced

- **No trading math in UI.** EMAs, equity curves, drawdown, win rate, profit
  factor, P&L are precomputed in `src/mock/` (the same layer the backend will
  replace). Components only format (`src/lib/formatters.ts`, `Intl.NumberFormat`)
  and render.
- **Server state never copied into Zustand.** Stores hold UI selections only;
  data lives in TanStack Query.
- **API boundary parsing.** `http*` services parse responses with Zod; errors
  normalize to `{ error: { code, message, request_id } }` via `ApiError`.
- **All timestamps ISO-8601 UTC** at the API layer; converted at display time.

## Connecting the Go backend

1. Set `VITE_DATA_MODE=api` (and `VITE_API_URL`) — or change the API URL in
   Settings.
2. `createServices('api')` already returns the `http*` implementations; no UI
   changes needed. Endpoints the frontend expects are listed above — keep
   response shapes aligned with `src/types/domain.ts`.
3. Realtime: the live market stream is already wired — `useLiveStream`
   opens one managed WebSocket to `/api/market/live/stream` (URL derived
   from `VITE_API_URL`) and folds status/quote/bar frames into TanStack
   Query cache. The generic `RealtimeClient` contract
   (`connect`/`disconnect`/`subscribe`/`onStatusChange`, channels
   `market.candles | strategy.signals | backtest.progress`, envelope
   `{ type, channel, timestamp, payload }`) remains a `NoopRealtimeClient`
   in `src/services/index.ts` for those not-yet-served channels.

## Live market data

The Market page has a **Historical | Live** source toggle. Live mode renders
provider-backed XAU/USD data from the backend's managed Twelve Data feed
(`/api/market/live/*`) — the API key never reaches the browser and the page
only polls the backend's cached state (status 10s, quote 5s, candles 10s).

- Live mode requires `LIVE_MARKET_ENABLED=true` + `TWELVE_DATA_API_KEY` on the
  backend; otherwise the panel reports "Live data is not configured".
- Connection, stale-data, and provider errors surface truthfully (no fake
  prices); bid/ask/day volume only render when the provider supplies them.
- Mock mode reports live as disabled — demo prices are never presented as
  live data.
- XAUUSD is the only instrument; other assets are rejected by the backend.

## Known limitations (Phase 1)

- The generic `RealtimeClient` service stays a noop (`Realtime: disabled`) —
  future channels beyond the live market stream are not served yet; the
  Market page's live stream itself is real WebSocket (`useLiveStream`).
- `http*` services are thin pass-throughs; they work only when the backend
  endpoints exist.
- Mock candles are synthetic and end at a fixed historical date (2024-12-31);
  the "latest" quote is historical, never live.
- Trade pagination is client-page-shaped (mock); it maps 1:1 to the planned
  `page`/`pageSize` API params.
