import type {
  Asset,
  BacktestDetail,
  BacktestRequest,
  BacktestRun,
  CandleSeries,
  Comparison,
  ComparisonRequest,
  LiveCandles,
  LiveQuote,
  LiveStatus,
  WalkForward,
  WalkForwardRequest,
  SignalResearch,
  SignalResearchRequest,
  DashboardSummary,
  MarketSummary,
  PagedTrades,
  Signal,
  Strategy,
  StrategyListItem,
  Timeframe,
  TradeFilters,
  CreateGraphRequest,
  GraphBacktestRequest,
  GraphDefinition,
	GraphDiagnostics, GraphDiagnosticsRequest, GraphRevision,
  StrategyGraph,
  ValidateGraphResponse,
  ResearchStudy,
  ResearchStudyRequest,
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

/**
 * Provider-backed live market observation (XAUUSD only). Distinct from
 * MarketService on purpose: historical research data and live market
 * observation never share a path.
 */
export interface LiveMarketService {
  getStatus(): Promise<LiveStatus>;
  getQuote(assetId: string): Promise<LiveQuote>;
  getCandles(assetId: string, timeframe: Timeframe, limit?: number): Promise<LiveCandles>;
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

export interface ComparisonService {
  run(request: ComparisonRequest): Promise<Comparison>;
  list(): Promise<Comparison[]>;
  get(id: string): Promise<Comparison>;
}

export interface WalkForwardService {
  preview(request: WalkForwardRequest): Promise<WalkForward>;
  run(request: WalkForwardRequest): Promise<WalkForward>;
  list(): Promise<WalkForward[]>;
  get(id: string): Promise<WalkForward>;
}

export interface HealthService {
  check(): Promise<HealthStatus>;
}

export interface GraphService {
  list(): Promise<StrategyGraph[]>;
  validate(definition: GraphDefinition): Promise<ValidateGraphResponse>;
  create(request: CreateGraphRequest): Promise<StrategyGraph>;
  get(id: string): Promise<StrategyGraph>;
  listRevisions(id: string): Promise<GraphRevision[]>;
  createRevision(id: string, definition: GraphDefinition): Promise<GraphRevision>;
  runBacktest(id: string, revision: number, request: GraphBacktestRequest): Promise<BacktestRun>;
  diagnostics(id: string, revision: number, request: GraphDiagnosticsRequest): Promise<GraphDiagnostics>;
}

export interface SignalResearchService {
  run(request: SignalResearchRequest): Promise<SignalResearch>;
  list(): Promise<SignalResearch[]>;
  get(id: string): Promise<SignalResearch>;
}

export interface ResearchStudyService {
  create(request: ResearchStudyRequest): Promise<ResearchStudy>;
  update(id: string, request: ResearchStudyRequest): Promise<ResearchStudy>;
  list(): Promise<ResearchStudy[]>;
  get(id: string): Promise<ResearchStudy>;
  freeze(id: string): Promise<ResearchStudy>;
  runExploratory(id: string): Promise<ResearchStudy>;
  validateHoldout(id: string): Promise<ResearchStudy>;
}
