import type {
  Asset,
  BacktestDetail,
  BacktestRequest,
  BacktestRun,
  CandleSeries,
  DashboardSummary,
  MarketSummary,
  PagedTrades,
  Signal,
  Strategy,
  StrategyListItem,
  Timeframe,
  TradeFilters,
} from '@/types/domain';
import type { HealthStatus } from '@/types/api';

/**
 * Service contracts. Components and hooks depend on these interfaces only;
 * `src/services/index.ts` picks mock or http implementations from
 * `VITE_DATA_MODE`.
 */

export interface MarketService {
  getAssets(): Promise<Asset[]>;
  getCandles(assetId: string, timeframe: Timeframe): Promise<CandleSeries>;
  getMarketSummary(assetId: string, timeframe: Timeframe): Promise<MarketSummary>;
}

export interface StrategyService {
  list(): Promise<StrategyListItem[]>;
  get(id: string): Promise<Strategy>;
  getSignals(opts?: { assetId?: string; strategyId?: string; limit?: number }): Promise<Signal[]>;
}

export interface BacktestService {
  listRuns(): Promise<BacktestRun[]>;
  runsForStrategy(strategyId: string): Promise<BacktestRun[]>;
  getRun(runId: string): Promise<BacktestDetail>;
  getLatestRunId(): Promise<string | null>;
  getSummary(): Promise<DashboardSummary>;
  run(request: BacktestRequest): Promise<BacktestRun>;
}

export interface TradeService {
  getTrades(filters: TradeFilters): Promise<PagedTrades>;
}

export interface HealthService {
  check(): Promise<HealthStatus>;
}
