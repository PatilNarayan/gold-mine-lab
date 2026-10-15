import type {
  BacktestDetail,
  BacktestMetrics,
  BacktestRequest,
  BacktestRun,
  BacktestTrade,
  DashboardSummary,
  EquityPoint,
  ParameterValues,
  StrategyPerformance,
} from '@/types/domain';
import { generateCandleSeries, HISTORY_END } from './candles';
import { strategyById } from './strategies';
import { rngFor, randBetween, randInt, round } from './seed';
import { assetMeta } from './assets';

/**
 * Backtest runs, trades, metrics and equity curves are fully precomputed here
 * — the UI never derives trading statistics itself.
 */

function defaultParameters(strategyId: string): ParameterValues {
  const s = strategyById(strategyId);
  const out: ParameterValues = {};
  for (const p of s?.parameters ?? []) out[p.name] = p.default;
  return out;
}

interface SeedSpec {
  strategyId: string;
  assetId: string;
  status: BacktestRun['status'];
  startDate: string;
  endDate: string;
  daysAgo: number; // offset into the historical window for createdAt
}

const SEED_SPECS: SeedSpec[] = [
  { strategyId: 'ema-cross', assetId: 'xauusd', status: 'completed', startDate: '2024-02-01', endDate: '2024-11-30', daysAgo: 10 },
  { strategyId: 'ema-cross', assetId: 'xagusd', status: 'completed', startDate: '2024-03-01', endDate: '2024-12-15', daysAgo: 8 },
  { strategyId: 'rsi-mean-reversion', assetId: 'xauusd', status: 'completed', startDate: '2024-04-01', endDate: '2024-10-31', daysAgo: 6 },
  { strategyId: 'macd-trend', assetId: 'msft', status: 'completed', startDate: '2024-01-10', endDate: '2024-12-01', daysAgo: 5 },
  { strategyId: 'macd-trend', assetId: 'tsla', status: 'completed', startDate: '2024-02-15', endDate: '2024-12-20', daysAgo: 4 },
  { strategyId: 'atr-breakout', assetId: 'xauusd', status: 'completed', startDate: '2024-05-01', endDate: '2024-11-15', daysAgo: 3 },
  { strategyId: 'bollinger-squeeze', assetId: 'xagusd', status: 'failed', startDate: '2024-06-01', endDate: '2024-12-01', daysAgo: 2 },
  { strategyId: 'rsi-mean-reversion', assetId: 'xauusd', status: 'completed', startDate: '2024-07-01', endDate: '2024-12-28', daysAgo: 1 },
];

function seedRuns(): BacktestRun[] {
  return SEED_SPECS.map((spec, i) => {
    const strategy = strategyById(spec.strategyId)!;
    const created = new Date(Date.parse(HISTORY_END) - spec.daysAgo * 86_400_000);
    const completed = spec.status === 'completed' ? new Date(created.getTime() + 90_000) : null;
    return {
      id: `run-${String(i + 1).padStart(3, '0')}`,
      strategyId: spec.strategyId,
      assetId: spec.assetId,
      timeframe: strategy.timeframe,
      startDate: spec.startDate,
      endDate: spec.endDate,
      initialCapital: 100_000,
      positionSize: 5,
      commission: 0.02,
      slippage: 0.05,
      parameters: defaultParameters(spec.strategyId),
      status: spec.status,
      createdAt: created.toISOString(),
      completedAt: completed ? completed.toISOString() : null,
      error:
        spec.status === 'failed'
          ? 'Parameter sweep produced zero trades — check date range and thresholds'
          : null,
    };
  });
}

const runRegistry = new Map<string, BacktestRun>();
const detailRegistry = new Map<string, BacktestDetail>();
let seeded = false;
let userRunCounter = 0;

function ensureSeeded(): void {
  if (seeded) return;
  seeded = true;
  for (const run of seedRuns()) runRegistry.set(run.id, run);
}

/** Deterministic equity curve + trades for a run. */
function buildDetail(run: BacktestRun): BacktestDetail {
  const rng = rngFor(`run|${run.id}|${run.strategyId}|${run.assetId}`);
  const strategy = strategyById(run.strategyId) ?? null;
  const meta = assetMeta(run.assetId);
  const series = generateCandleSeries(run.assetId, run.timeframe, 240);
  const candles = series.candles;

  if (run.status === 'failed' || !strategy) {
    return { run, strategy, metrics: null, equity: [], trades: [] };
  }

  // Equity curve: ~120 points between startDate and endDate.
  const start = Date.parse(run.startDate);
  const end = Date.parse(run.endDate);
  const points = 120;
  const stepMs = Math.max((end - start) / (points - 1), 3_600_000);
  const drift = randBetween(rng, -0.0018, 0.0042);
  const vol = randBetween(rng, 0.012, 0.032);

  const equity: EquityPoint[] = [];
  let eq = run.initialCapital;
  let peak = eq;
  for (let i = 0; i < points; i++) {
    if (i > 0) eq = eq * (1 + drift + (rng() - 0.5) * 2 * vol);
    peak = Math.max(peak, eq);
    equity.push({
      time: new Date(start + i * stepMs).toISOString(),
      equity: round(eq, 2),
      drawdown: round(eq / peak - 1, 4),
    });
  }
  const finalEquity = equity[equity.length - 1].equity;
  const netPnl = round(finalEquity - run.initialCapital, 2);
  const maxDrawdown = equity.reduce((m, p) => Math.min(m, p.drawdown), 0);

  // Trades: entries/exits sampled from the candle series.
  const tradeCount = randInt(rng, 16, 44);
  const trades: BacktestTrade[] = [];
  for (let i = 0; i < tradeCount; i++) {
    const entryIdx = randInt(rng, 5, candles.length - 30);
    const holding = randInt(rng, 2, 22);
    const exitIdx = Math.min(entryIdx + holding, candles.length - 1);
    const entry = candles[entryIdx];
    const exit = candles[exitIdx];
    const side: BacktestTrade['side'] = rng() < 0.55 ? 'BUY' : 'SELL';
    const qty = round(randBetween(rng, 0.25, 4), 2);
    const direction = side === 'BUY' ? 1 : -1;
    const cost = (run.commission + run.slippage) * 0.01 * entry.close * qty;
    const open = rng() < 0.07;
    const gross = (exit.close - entry.close) * qty * direction;
    const pnl = open ? null : round(gross - cost, 2);
    trades.push({
      id: `${run.id}-t${String(i + 1).padStart(3, '0')}`,
      runId: run.id,
      strategyId: run.strategyId,
      assetId: run.assetId,
      side,
      entryTime: entry.time,
      entryPrice: round(entry.close, meta.digits),
      exitTime: open ? null : exit.time,
      exitPrice: open ? null : round(exit.close, meta.digits),
      quantity: qty,
      pnl,
      status: open ? 'open' : 'closed',
      result: open ? 'open' : pnl !== null && pnl >= 0 ? 'win' : 'loss',
    });
  }

  const closed = trades.filter((t) => t.status === 'closed');
  const wins = closed.filter((t) => (t.pnl ?? 0) > 0);
  const losses = closed.filter((t) => (t.pnl ?? 0) <= 0);
  const grossWin = wins.reduce((s, t) => s + (t.pnl ?? 0), 0);
  const grossLoss = Math.abs(losses.reduce((s, t) => s + (t.pnl ?? 0), 0));
  const metrics: BacktestMetrics = {
    netPnl,
    returnPct: round(netPnl / run.initialCapital, 4),
    winRate: closed.length ? round(wins.length / closed.length, 4) : 0,
    profitFactor: grossLoss > 0 ? round(grossWin / grossLoss, 2) : wins.length ? 99 : 0,
    maxDrawdown: round(maxDrawdown, 4),
    totalTrades: closed.length,
    winningTrades: wins.length,
    losingTrades: losses.length,
    avgTradePnl: closed.length ? round(netPnl / closed.length, 2) : 0,
    grossProfit: round(grossWin, 2),
    grossLoss: round(grossLoss, 2),
    totalCommissions: round(
      closed.reduce((s, t) => s + (t.entryCommission ?? 0) + (t.exitCommission ?? 0), 0),
      2,
    ),
  };

  return { run, strategy, metrics, equity, trades };
}

export function getRunDetail(runId: string): BacktestDetail | null {
  ensureSeeded();
  const run = runRegistry.get(runId);
  if (!run) return null;
  let detail = detailRegistry.get(runId);
  if (!detail) {
    detail = buildDetail(run);
    detailRegistry.set(runId, detail);
  }
  return detail;
}

export function listRuns(): BacktestRun[] {
  ensureSeeded();
  return [...runRegistry.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

export function runsForStrategy(strategyId: string): BacktestRun[] {
  return listRuns().filter((r) => r.strategyId === strategyId);
}

export function allTrades(): BacktestTrade[] {
  ensureSeeded();
  return [...runRegistry.values()]
    .filter((r) => r.status === 'completed')
    .flatMap((r) => getRunDetail(r.id)?.trades ?? [])
    .sort((a, b) => b.entryTime.localeCompare(a.entryTime));
}

/** Execute a simulated run and register it so /results/:runId resolves. */
export function executeBacktest(req: BacktestRequest): BacktestRun {
  ensureSeeded();
  const strategy = strategyById(req.strategyId);
  const now = new Date().toISOString();
  const run: BacktestRun = {
    id: `run-user-${++userRunCounter}`,
    strategyId: req.strategyId,
    assetId: req.assetId,
    timeframe: req.timeframe,
    startDate: req.startDate,
    endDate: req.endDate,
    initialCapital: req.initialCapital,
    positionSize: req.positionSize,
    commission: req.commission,
    slippage: req.slippage,
    parameters: req.parameters,
    status: strategy ? 'completed' : 'failed',
    createdAt: now,
    completedAt: strategy ? now : null,
    error: strategy ? null : `Unknown strategy "${req.strategyId}"`,
  };
  runRegistry.set(run.id, run);
  detailRegistry.set(run.id, buildDetail(run));
  return run;
}

export function latestCompletedRunId(): string | null {
  return listRuns().find((r) => r.status === 'completed')?.id ?? null;
}

export function dashboardSummary(): DashboardSummary {
  ensureSeeded();
  const completed = [...runRegistry.values()]
    .filter((r) => r.status === 'completed')
    .map((r) => getRunDetail(r.id)!)
    .filter((d) => d.metrics);

  const totalBacktests = completed.length;
  // Latest completed run — independent runs are NOT a portfolio, so the
  // dashboard shows the most recent experiment's figures and curve only.
  const latest = [...completed].sort((a, b) =>
    (b.run.completedAt ?? '').localeCompare(a.run.completedAt ?? ''),
  )[0];
  const latestStrategy = latest ? strategyById(latest.run.strategyId) : null;

  return {
    totalBacktests,
    latestRunId: latest?.run.id ?? null,
    latestRunStrategyId: latest?.run.strategyId ?? null,
    latestRunStrategyName: latestStrategy?.name ?? null,
    winRate: latest?.metrics?.winRate ?? 0,
    netPnl: latest?.metrics?.netPnl ?? 0,
    maxDrawdown: latest?.metrics?.maxDrawdown ?? 0,
    equity: latest?.equity ?? [],
  };
}

// Latest-run semantics, mirroring the real service: experiments are not
// summed — figures are the newest completed run's own metrics.
export function strategyPerformance(strategyId: string): StrategyPerformance {
  const runs = runsForStrategy(strategyId)
    .filter((r) => r.status === 'completed')
    .sort((a, b) => (b.completedAt ?? '').localeCompare(a.completedAt ?? ''));
  const latest = runs[0] ? getRunDetail(runs[0].id) : undefined;
  return {
    totalBacktests: runs.length,
    latestRunId: latest?.run.id ?? null,
    latestRunTimeframe: latest?.run.timeframe ?? null,
    latestRunStartDate: latest?.run.startDate ?? null,
    latestRunEndDate: latest?.run.endDate ?? null,
    latestRunNetPnl: latest?.metrics?.netPnl ?? 0,
    latestRunWinRate: latest?.metrics?.winRate ?? 0,
  };
}
