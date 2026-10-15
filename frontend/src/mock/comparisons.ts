import type {
  Comparison,
  ComparisonRequest,
  ComparisonResult,
} from '@/types/domain';
import { executeBacktest, getRunDetail } from './backtests';

/**
 * Mock comparison store — same semantics as the backend: legs run the shared
 * mock engine under identical assumptions; results persist for the session.
 */
const registry = new Map<string, Comparison>();
let counter = 0;

export function executeComparison(req: ComparisonRequest): Comparison {
  const now = new Date().toISOString();
  const id = `cmp-${++counter}`;
  const windows: { sample: 'in_sample' | 'out_of_sample'; start: string; end: string }[] = [
    { sample: 'in_sample', start: req.startDate, end: req.endDate },
  ];
  if (req.oosStartDate && req.oosEndDate) {
    windows.push({ sample: 'out_of_sample', start: req.oosStartDate, end: req.oosEndDate });
  }

  const results: ComparisonResult[] = [];
  let failures = 0;
  for (const leg of req.legs) {
    for (const w of windows) {
      const run = executeBacktest({
        assetId: req.assetId,
        strategyId: leg.strategyId,
        timeframe: req.timeframe,
        startDate: w.start,
        endDate: w.end,
        initialCapital: req.initialCapital,
        positionSize: req.positionSize,
        commission: req.commission,
        slippage: req.slippage,
        parameters: leg.parameters,
      });
      const detail = getRunDetail(run.id);
      results.push({
        run,
        sample: w.sample,
        metrics: detail?.metrics ?? null,
        equity: detail?.equity ?? [],
      });
      if (run.status !== 'completed') failures++;
    }
  }

  const cmp: Comparison = {
    id,
    status: failures === 0 ? 'completed' : failures === results.length ? 'failed' : 'partial_failed',
    assetId: req.assetId,
    timeframe: req.timeframe,
    startDate: req.startDate,
    endDate: req.endDate,
    oosStartDate: req.oosStartDate ?? null,
    oosEndDate: req.oosEndDate ?? null,
    initialCapital: req.initialCapital,
    positionSize: req.positionSize,
    commission: req.commission,
    slippage: req.slippage,
    strategyIds: req.legs.map((l) => l.strategyId),
    error: failures ? `${failures} of ${results.length} legs failed` : null,
    createdAt: now,
    completedAt: now,
    results,
  };
  registry.set(id, cmp);
  return cmp;
}

export function listComparisons(): Comparison[] {
  return [...registry.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

export function getComparison(id: string): Comparison | null {
  return registry.get(id) ?? null;
}
