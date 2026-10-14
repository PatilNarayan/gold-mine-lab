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
export const strategyPerformanceSchema = z.object({
  netPnl: z.number(),
  winRate: z.number(),
  totalBacktests: z.number(),
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
  profitFactor: z.number(),
  maxDrawdown: z.number(),
  totalTrades: z.number(),
  avgTradePnl: z.number(),
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
