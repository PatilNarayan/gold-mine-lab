import { z } from 'zod';

/**
 * Domain model mirroring the Go backend's planned schema. All timestamps are
 * ISO-8601 UTC strings at this boundary; money and percentages are numbers.
 * Zod schemas are used to parse service responses at the boundary.
 */

export const timeframeSchema = z.enum(['1m', '5m', '15m', '1h', '4h', '1d']);
export type Timeframe = z.infer<typeof timeframeSchema>;

export const assetClassSchema = z.enum(['metal', 'equity', 'forex']);
export type AssetClass = z.infer<typeof assetClassSchema>;

export const assetSchema = z.object({
  id: z.string(),
  symbol: z.string(),
  name: z.string(),
  assetClass: assetClassSchema,
  quoteCurrency: z.string(),
});
export type Asset = z.infer<typeof assetSchema>;

export const candleSchema = z.object({
  time: z.string(),
  open: z.number(),
  high: z.number(),
  low: z.number(),
  close: z.number(),
  volume: z.number(),
});
export type Candle = z.infer<typeof candleSchema>;

export const seriesPointSchema = z.object({
  time: z.string(),
  value: z.number(),
});
export type SeriesPoint = z.infer<typeof seriesPointSchema>;

/** Candle payload with precomputed overlays, as the backend will return it. */
export const candleSeriesSchema = z.object({
  assetId: z.string(),
  timeframe: timeframeSchema,
  candles: z.array(candleSchema),
  overlays: z.object({
    ema20: z.array(seriesPointSchema),
    ema50: z.array(seriesPointSchema),
  }),
});
export type CandleSeries = z.infer<typeof candleSeriesSchema>;

export const marketStatusSchema = z.enum(['Historical / Demo', 'Historical', 'Open', 'Closed']);
export type MarketStatus = z.infer<typeof marketStatusSchema>;

export const marketSummarySchema = z.object({
  asset: assetSchema,
  timeframe: timeframeSchema,
  lastClose: z.number(),
  change: z.number(),
  changePercent: z.number(),
  asOf: z.string(),
  status: marketStatusSchema,
});
export type MarketSummary = z.infer<typeof marketSummarySchema>;

export const parameterFieldSchema = z.object({
  name: z.string(),
  label: z.string(),
  type: z.enum(['number', 'select', 'boolean']),
  default: z.union([z.number(), z.string(), z.boolean()]),
  min: z.number().optional(),
  max: z.number().optional(),
  step: z.number().optional(),
  options: z.array(z.string()).optional(),
  description: z.string().optional(),
});
export type ParameterField = z.infer<typeof parameterFieldSchema>;

export const strategyStatusSchema = z.enum(['active', 'inactive']);
export type StrategyStatus = z.infer<typeof strategyStatusSchema>;

export const strategySchema = z.object({
  id: z.string(),
  name: z.string(),
  version: z.string(),
  description: z.string(),
  supportedAssets: z.array(z.string()),
  timeframe: timeframeSchema, // default; runs may pick any of supportedTimeframes
  supportedTimeframes: z.array(timeframeSchema).optional(),
  parameters: z.array(parameterFieldSchema),
  status: strategyStatusSchema,
});
export type Strategy = z.infer<typeof strategySchema>;

export const signalActionSchema = z.enum(['BUY', 'SELL', 'NEUTRAL']);
export type SignalAction = z.infer<typeof signalActionSchema>;

export const signalSchema = z.object({
  id: z.string(),
  strategyId: z.string(),
  assetId: z.string(),
  action: signalActionSchema,
  score: z.number(),
  price: z.number(),
  timestamp: z.string(),
  reason: z.string(),
  metadata: z.record(z.unknown()).optional(),
});
export type Signal = z.infer<typeof signalSchema>;

/** Precomputed per-strategy stats attached to list rows. */
/** Per-strategy experiment count + LATEST completed run's metrics —
 *  independent runs are not summed/averaged into a portfolio result. */
export const strategyPerformanceSchema = z.object({
  totalBacktests: z.number(),
  latestRunId: z.string().nullable(),
  latestRunTimeframe: z.string().nullable(),
  latestRunStartDate: z.string().nullable(),
  latestRunEndDate: z.string().nullable(),
  latestRunNetPnl: z.number(),
  latestRunWinRate: z.number(),
});
export type StrategyPerformance = z.infer<typeof strategyPerformanceSchema>;

export const strategyListItemSchema = strategySchema.extend({
  lastSignal: signalSchema.nullable(),
  performance: strategyPerformanceSchema,
});
export type StrategyListItem = z.infer<typeof strategyListItemSchema>;

export const backtestStatusSchema = z.enum(['queued', 'running', 'completed', 'failed']);
export type BacktestStatus = z.infer<typeof backtestStatusSchema>;

/** Mirrors the planned `backtest_metrics` table. */
export const backtestMetricsSchema = z.object({
  netPnl: z.number(),
  returnPct: z.number(),
  winRate: z.number(),
  profitFactor: z.number().nullable(),
  maxDrawdown: z.number(),
  totalTrades: z.number(),
  winningTrades: z.number(),
  losingTrades: z.number(),
  avgTradePnl: z.number(),
  grossProfit: z.number(),
  grossLoss: z.number(),
  totalCommissions: z.number().nullable(), // null on legacy runs
  // Risk metrics (Phase 6a): null when undefined — no qualifying trades,
  // a zero denominator, or a legacy run lacking the metric row.
  avgWin: z.number().nullable(),             // avg NET pnl of winning trades
  avgLoss: z.number().nullable(),            // avg NET loss, positive magnitude
  payoffRatio: z.number().nullable(),        // avgWin / avgLoss
  expectancy: z.number().nullable(),         // net pnl per closed trade
  recoveryFactor: z.number().nullable(),     // netPnl / max DD amount
  timeInMarketPct: z.number().nullable(),    // % of eval-window bars in-market
  maxConsecutiveLosses: z.number().nullable(),
  barsInMarket: z.number().nullable(),
  eligibleBars: z.number().nullable(),
});
export type BacktestMetrics = z.infer<typeof backtestMetricsSchema>;

export const parameterValuesSchema = z.record(z.union([z.number(), z.string(), z.boolean()]));
export type ParameterValues = z.infer<typeof parameterValuesSchema>;

/** Mirrors the planned `backtest_runs` table. */
export const backtestRunSchema = z.object({
  id: z.string(),
  strategyId: z.string(),
  assetId: z.string(),
  timeframe: timeframeSchema,
  startDate: z.string(),
  endDate: z.string(),
  initialCapital: z.number(),
  positionSize: z.number(),
  commission: z.number(),
  slippage: z.number(),
  parameters: parameterValuesSchema,
  status: backtestStatusSchema,
  createdAt: z.string(),
  completedAt: z.string().nullable(),
  error: z.string().nullable(),
  // Provenance — present on engine ≥2.1 runs, null on legacy rows.
  engineVersion: z.string().nullable().optional(),
  strategyVersion: z.string().nullable().optional(),
  dataset: z.string().nullable().optional(),
});
export type BacktestRun = z.infer<typeof backtestRunSchema>;

export const tradeResultSchema = z.enum(['win', 'loss', 'open']);
export type TradeResult = z.infer<typeof tradeResultSchema>;

export const tradeSideSchema = z.enum(['BUY', 'SELL']);
export type TradeSide = z.infer<typeof tradeSideSchema>;

/** Mirrors the planned `backtest_trades` table. */
export const backtestTradeSchema = z.object({
  id: z.string(),
  runId: z.string(),
  strategyId: z.string(),
  assetId: z.string(),
  side: tradeSideSchema,
  entryTime: z.string(),
  entryPrice: z.number(),
  exitTime: z.string().nullable(),
  exitPrice: z.number().nullable(),
  quantity: z.number(),
  grossPnl: z.number().nullable().optional(), // null on legacy runs
  pnl: z.number().nullable(), // net of commissions (engine >= 2.1)
  entryCommission: z.number().nullable().optional(),
  exitCommission: z.number().nullable().optional(),
  status: z.enum(['open', 'closed']),
  result: tradeResultSchema,
});
export type BacktestTrade = z.infer<typeof backtestTradeSchema>;

export const equityPointSchema = z.object({
  time: z.string(),
  equity: z.number(),
  drawdown: z.number(), // fraction <= 0, e.g. -0.083 means -8.3%
});
export type EquityPoint = z.infer<typeof equityPointSchema>;

export const backtestDetailSchema = z.object({
  run: backtestRunSchema,
  strategy: strategySchema.nullable(),
  metrics: backtestMetricsSchema.nullable(),
  equity: z.array(equityPointSchema),
  trades: z.array(backtestTradeSchema),
});
export type BacktestDetail = z.infer<typeof backtestDetailSchema>;

/** Latest-run snapshot for the dashboard — independent runs are not a
 *  portfolio; figures + equity describe the latest completed run only. */
export const dashboardSummarySchema = z.object({
  totalBacktests: z.number(),
  latestRunId: z.string().nullable(),
  latestRunStrategyId: z.string().nullable(),
  latestRunStrategyName: z.string().nullable(),
  winRate: z.number(),
  netPnl: z.number(),
  maxDrawdown: z.number(),
  equity: z.array(equityPointSchema),
});
export type DashboardSummary = z.infer<typeof dashboardSummarySchema>;

// --- comparisons (Phase 4 research grouping) ---------------------------------

export const comparisonLegSchema = z.object({
  strategyId: z.string(),
  parameters: parameterValuesSchema,
});
export type ComparisonLeg = z.infer<typeof comparisonLegSchema>;

/** POST /api/comparisons — identical assumptions across legs; per-leg params. */
export const comparisonRequestSchema = z.object({
  legs: z.array(comparisonLegSchema).min(2),
  assetId: z.string(),
  timeframe: timeframeSchema,
  startDate: z.string(), // in-sample window
  endDate: z.string(),
  oosStartDate: z.string().nullable().optional(), // optional out-of-sample
  oosEndDate: z.string().nullable().optional(),
  initialCapital: z.number(),
  positionSize: z.number(),
  commission: z.number(),
  slippage: z.number(),
});
export type ComparisonRequest = z.infer<typeof comparisonRequestSchema>;

export const comparisonStatusSchema = z.enum([
  'pending',
  'running',
  'completed',
  'partial_failed',
  'failed',
]);
export type ComparisonStatus = z.infer<typeof comparisonStatusSchema>;

export const comparisonResultSchema = z.object({
  run: backtestRunSchema,
  sample: z.enum(['in_sample', 'out_of_sample', '']),
  metrics: backtestMetricsSchema.nullable(),
  equity: z.array(equityPointSchema),
});
export type ComparisonResult = z.infer<typeof comparisonResultSchema>;

export const comparisonSchema = z.object({
  id: z.string(),
  status: comparisonStatusSchema,
  assetId: z.string(),
  timeframe: z.string(),
  startDate: z.string(),
  endDate: z.string(),
  oosStartDate: z.string().nullable(),
  oosEndDate: z.string().nullable(),
  initialCapital: z.number(),
  positionSize: z.number(),
  commission: z.number(),
  slippage: z.number(),
  strategyIds: z.array(z.string()),
  error: z.string().nullable(),
  createdAt: z.string(),
  completedAt: z.string().nullable(),
  results: z.array(comparisonResultSchema).optional(),
});
export type Comparison = z.infer<typeof comparisonSchema>;

export const backtestRequestSchema = z.object({
  assetId: z.string(),
  strategyId: z.string(),
  timeframe: timeframeSchema,
  startDate: z.string(),
  endDate: z.string(),
  initialCapital: z.number(),
  positionSize: z.number(),
  commission: z.number(),
  slippage: z.number(),
  parameters: parameterValuesSchema,
});
export type BacktestRequest = z.infer<typeof backtestRequestSchema>;

export const tradeFiltersSchema = z.object({
  strategyId: z.string().optional(),
  side: tradeSideSchema.optional(),
  result: tradeResultSchema.optional(),
  from: z.string().optional(),
  to: z.string().optional(),
  page: z.number().int().min(1).default(1),
  pageSize: z.number().int().min(1).max(200).default(25),
});
export type TradeFilters = z.infer<typeof tradeFiltersSchema>;

export const pagedTradesSchema = z.object({
  items: z.array(backtestTradeSchema),
  total: z.number(),
  page: z.number(),
  pageSize: z.number(),
});
export type PagedTrades = z.infer<typeof pagedTradesSchema>;

/** POST /api/walkforwards — fixed-config walk-forward: identical assumptions
 * across legs and folds; per-leg params stay constant (no optimization). */
export const walkForwardRequestSchema = z.object({
  legs: z.array(comparisonLegSchema).min(1),
  assetId: z.string(),
  timeframe: timeframeSchema,
  startDate: z.string(), // overall research range
  endDate: z.string(),
  inSampleMonths: z.number().int().min(1),
  oosMonths: z.number().int().min(1),
  stepMonths: z.number().int().min(0), // 0 → defaults to oosMonths
  initialCapital: z.number(),
  positionSize: z.number(),
  commission: z.number(),
  slippage: z.number(),
});
export type WalkForwardRequest = z.infer<typeof walkForwardRequestSchema>;

export const walkForwardFoldSchema = z.object({
  foldIndex: z.number().int(),
  strategyId: z.string(),
  isStart: z.string(),
  isEnd: z.string(),
  oosStart: z.string(),
  oosEnd: z.string(),
  status: z.enum(['pending', 'completed', 'failed']),
  error: z.string().nullable().optional(),
  isResult: comparisonResultSchema.nullable().optional(),
  oosResult: comparisonResultSchema.nullable().optional(),
});
export type WalkForwardFold = z.infer<typeof walkForwardFoldSchema>;

export const walkForwardSummarySchema = z.object({
  completedFolds: z.number().int(),
  failedFolds: z.number().int(),
  positiveFolds: z.number().int(),
  medianReturnPct: z.number(),
  worstReturnPct: z.number(),
  worstMaxDrawdown: z.number(),
  totalTrades: z.number().int(),
  lowTradeFolds: z.number().int(),
  inconclusive: z.boolean(),
  // Fold-level risk aggregates over eligible OOS folds (Phase 6a) —
  // transparent stats, never pooled trades.
  medianExpectancy: z.number().nullable(),
  medianTimeInMarketPct: z.number().nullable(),
  worstConsecutiveLosses: z.number().nullable(),
  missingMetricFolds: z.number().int(),
});
export type WalkForwardSummary = z.infer<typeof walkForwardSummarySchema>;

export const walkForwardSchema = z.object({
  id: z.string(),
  status: comparisonStatusSchema,
  assetId: z.string(),
  timeframe: z.string(),
  startDate: z.string(),
  endDate: z.string(),
  inSampleMonths: z.number(),
  oosMonths: z.number(),
  stepMonths: z.number(),
  initialCapital: z.number(),
  positionSize: z.number(),
  commission: z.number(),
  slippage: z.number(),
  strategyIds: z.array(z.string()).optional(),
  error: z.string().nullable(),
  createdAt: z.string().optional(),
  completedAt: z.string().nullable().optional(),
  folds: z.array(walkForwardFoldSchema).optional(),
  summaries: z.record(z.string(), walkForwardSummarySchema).nullable().optional(),
});
export type WalkForward = z.infer<typeof walkForwardSchema>;

// --- Strategy graphs (Phase 6b) ----------------------------------------------

export const graphNodeTypeSchema = z.enum(['strategy', 'and', 'or', 'threshold', 'output']);
export type GraphNodeType = z.infer<typeof graphNodeTypeSchema>;

export const graphNodeSchema = z.object({
  id: z.string().min(1),
  type: graphNodeTypeSchema,
  strategyId: z.string().optional(),
  parameters: z.record(z.string(), z.unknown()).optional(),
  threshold: z.number().min(0).max(1).optional(),
});
export type GraphNode = z.infer<typeof graphNodeSchema>;

export const graphEdgeSchema = z.object({ from: z.string(), to: z.string() });
export type GraphEdge = z.infer<typeof graphEdgeSchema>;

export const graphDefinitionSchema = z.object({
  nodes: z.array(graphNodeSchema),
  edges: z.array(graphEdgeSchema),
});
export type GraphDefinition = z.infer<typeof graphDefinitionSchema>;

export const strategyGraphSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string(),
  status: z.string(),
  revisions: z.number(),
  createdAt: z.string(),
});
export type StrategyGraph = z.infer<typeof strategyGraphSchema>;

export const graphRevisionSchema = z.object({
  graphId: z.string(),
  revision: z.number(),
  definition: graphDefinitionSchema,
  definitionHash: z.string(),
  createdAt: z.string(),
});
export type GraphRevision = z.infer<typeof graphRevisionSchema>;

export const graphFieldErrorSchema = z.object({
  nodeId: z.string().optional(),
  message: z.string(),
});
export type GraphFieldError = z.infer<typeof graphFieldErrorSchema>;

export const validateGraphResponseSchema = z.object({
  valid: z.boolean(),
  errors: z.array(graphFieldErrorSchema),
});
export type ValidateGraphResponse = z.infer<typeof validateGraphResponseSchema>;

export const createGraphRequestSchema = z.object({
  name: z.string().min(1),
  description: z.string(),
  definition: graphDefinitionSchema,
});
export type CreateGraphRequest = z.infer<typeof createGraphRequestSchema>;

export const graphBacktestRequestSchema = z.object({
  assetId: z.string(),
  timeframe: timeframeSchema,
  startDate: z.string(),
  endDate: z.string(),
  initialCapital: z.number(),
  positionSize: z.number(),
  commission: z.number(),
  slippage: z.number(),
});
export type GraphBacktestRequest = z.infer<typeof graphBacktestRequestSchema>;

// --- Graph diagnostics (Phase 7 signal inspector) ----------------------------

export const graphDiagnosticsRequestSchema = z.object({
  assetId: z.string(),
  timeframe: timeframeSchema,
  startDate: z.string(),
  endDate: z.string(),
  traceLimit: z.number().int().min(0).max(2000).optional(),
});
export type GraphDiagnosticsRequest = z.infer<typeof graphDiagnosticsRequestSchema>;

export const reasonCountSchema = z.object({ reason: z.string(), count: z.number() });

export const graphNodeSummarySchema = z.object({
  nodeId: z.string(),
  nodeType: graphNodeTypeSchema,
  evaluatedBars: z.number(),
  buy: z.number(),
  sell: z.number(),
  neutral: z.number(),
  actionablePct: z.number(),
  avgScore: z.number().nullable().optional(),
  topReasons: z.array(reasonCountSchema),
});
export type GraphNodeSummary = z.infer<typeof graphNodeSummarySchema>;

export const graphTraceEntrySchema = z.object({
  nodeId: z.string(),
  action: signalActionSchema,
  score: z.number(),
  reason: z.string(),
});

export const graphTraceRowSchema = z.object({
  timestamp: z.string(),
  nodes: z.array(graphTraceEntrySchema),
});
export type GraphTraceRow = z.infer<typeof graphTraceRowSchema>;

export const graphDiagnosticsSchema = z.object({
  graphId: z.string(),
  revision: z.number(),
  hash: z.string(),
  assetId: z.string(),
  timeframe: timeframeSchema,
  startDate: z.string(),
  endDate: z.string(),
  evaluatedBars: z.number(),
  warmupBars: z.number(),
  dataset: z.string().nullable().optional(),
  lastCandle: z.string().nullable().optional(),
  traceLimit: z.number(),
  truncated: z.boolean(),
  nodes: z.array(graphNodeSummarySchema),
  traces: z.array(graphTraceRowSchema),
});
export type GraphDiagnostics = z.infer<typeof graphDiagnosticsSchema>;

/* ------------------------------- Phase 9: signal research ---------------- */

export const signalResearchLegSchema = z.object({
  strategyId: z.string(),
  parameters: z.record(z.any()).optional(),
});
export const signalResearchRequestSchema = z.object({
  name: z.string().optional(),
  legs: z.array(signalResearchLegSchema).min(2).max(8),
  assetId: z.string(),
  timeframe: z.string(),
  startDate: z.string(),
  endDate: z.string(),
  eventStudy: z.boolean(),
  regimeAnalysis: z.boolean().optional(),
});
export const signalRegimeStatsSchema = z.object({
  regime: z.string(),
  evaluatedBars: z.number(),
  buy: z.number(), sell: z.number(), neutral: z.number(),
  actionablePct: z.number(),
  buyEpisodes: z.number(), sellEpisodes: z.number(),
  avgScore: z.number().nullish(),
});
export const signalCandidateStatsSchema = z.object({
  strategyId: z.string(),
  version: z.string(),
  evaluatedBars: z.number(),
  buy: z.number(),
  sell: z.number(),
  neutral: z.number(),
  actionablePct: z.number(),
  buyEpisodes: z.number(),
  sellEpisodes: z.number(),
  avgScore: z.number().nullish(),
  byTrend: z.array(signalRegimeStatsSchema).nullish(),
  byVolatility: z.array(signalRegimeStatsSchema).nullish(),
});
export const signalRegimeAgreementSchema = z.object({
  regime: z.string(),
  bothBuy: z.number(), bothSell: z.number(),
  sameDirection: z.number(), opposite: z.number(),
  oneActionable: z.number(), bothNeutral: z.number(),
  coActionable: z.number(),
  agreementPct: z.number().nullish(),
  conflictPct: z.number().nullish(),
});
export const signalPairAgreementSchema = z.object({
  a: z.string(), b: z.string(),
  bothBuy: z.number(), bothSell: z.number(),
  sameDirection: z.number(), opposite: z.number(),
  oneActionable: z.number(), bothNeutral: z.number(),
  coActionable: z.number(),
  agreementPct: z.number().nullish(),
  conflictPct: z.number().nullish(),
  byTrend: z.array(signalRegimeAgreementSchema).nullish(),
  byVolatility: z.array(signalRegimeAgreementSchema).nullish(),
});
export const signalHorizonBenchmarkSchema = z.object({
  eventCount: z.number(), controlCount: z.number(), baselineCount: z.number(),
  eventMean: z.number().nullish(), eventMedian: z.number().nullish(),
  baselineMean: z.number().nullish(), baselineMedian: z.number().nullish(),
  diffMean: z.number().nullish(), diffMedian: z.number().nullish(),
  controlMean: z.number().nullish(), controlMedian: z.number().nullish(),
  diffMeanControl: z.number().nullish(), diffMedianControl: z.number().nullish(),
  eventPositivePct: z.number().nullish(), baselinePositivePct: z.number().nullish(),
  worstEventReturn: z.number().nullish(),
  ciStatus: z.string(),
  ciLow: z.number().nullish(), ciHigh: z.number().nullish(),
  validReplicates: z.number(), blockLength: z.number(),
});
export const signalHorizonResultSchema = z.object({
  horizon: z.number(), strategyId: z.string(),
  eligible: z.number(), excluded: z.number(),
  meanReturn: z.number().nullish(), medianReturn: z.number().nullish(),
  positivePct: z.number().nullish(), worstReturn: z.number().nullish(),
  benchmark: signalHorizonBenchmarkSchema.nullish(),
});
export const signalBenchmarkMetaSchema = z.object({
  eventStudyVersion: z.string(), baselineVersion: z.string(),
  bootstrapVersion: z.string(), blockLengthRule: z.string(),
  replicates: z.number(), seed: z.number(),
  minEvents: z.number(), minControls: z.number(), minReplicateRate: z.number(),
});
export const signalRegimeForwardSchema = z.object({
  regime: z.string(),
  horizons: z.array(signalHorizonResultSchema),
});
export const signalForwardResultSchema = z.object({
  strategyId: z.string(),
  horizons: z.array(signalHorizonResultSchema),
  byTrend: z.array(signalRegimeForwardSchema).nullish(),
  byVolatility: z.array(signalRegimeForwardSchema).nullish(),
});
export const signalResearchSchema = z.object({
  id: z.string(), status: z.string(), name: z.string(),
  assetId: z.string(), timeframe: z.string(),
  startDate: z.string(), endDate: z.string(), eventStudy: z.boolean(),
  dataset: z.string().nullish(), lastCandle: z.string().nullish(),
  evaluatedBars: z.number(), warmupBars: z.number(),
  strategyIds: z.array(z.string()),
  candidates: z.array(signalCandidateStatsSchema).nullish(),
  pairs: z.array(signalPairAgreementSchema).nullish(),
  forward: z.array(signalForwardResultSchema).nullish(),
  regimeVersion: z.string().nullish(),
  benchmarkMeta: signalBenchmarkMetaSchema.nullish(),
  error: z.string().nullish(),
  createdAt: z.string(),
});
export type SignalResearchLeg = z.infer<typeof signalResearchLegSchema>;
export type SignalResearchRequest = z.infer<typeof signalResearchRequestSchema>;
export type SignalCandidateStats = z.infer<typeof signalCandidateStatsSchema>;
export type SignalPairAgreement = z.infer<typeof signalPairAgreementSchema>;
export type SignalHorizonResult = z.infer<typeof signalHorizonResultSchema>;
export type SignalRegimeStats = z.infer<typeof signalRegimeStatsSchema>;
export type SignalRegimeAgreement = z.infer<typeof signalRegimeAgreementSchema>;
export type SignalRegimeForward = z.infer<typeof signalRegimeForwardSchema>;
export type SignalHorizonBenchmark = z.infer<typeof signalHorizonBenchmarkSchema>;
export type SignalBenchmarkMeta = z.infer<typeof signalBenchmarkMetaSchema>;
export type SignalForwardResult = z.infer<typeof signalForwardResultSchema>;
export type SignalResearch = z.infer<typeof signalResearchSchema>;
