import type { WalkForwardService } from '../interfaces';
import type {
  WalkForward,
  WalkForwardFold,
  WalkForwardRequest,
  WalkForwardSummary,
} from '@/types/domain';
import { executeBacktest, getRunDetail } from '../../mock/backtests';

/**
 * Mock walk-forward store — same semantics as the backend: fixed leg params,
 * rolling folds through the shared mock engine; persists for the session.
 */
const registry = new Map<string, WalkForward>();
let counter = 0;

function addMonths(iso: string, months: number): string {
  const d = new Date(iso + 'T00:00:00Z');
  const day = d.getUTCDate();
  d.setUTCMonth(d.getUTCMonth() + months);
  // Normalise month rollovers (e.g. Jan 31 + 1m) to the boundary we expect.
  if (d.getUTCDate() !== day) d.setUTCDate(0);
  return d.toISOString().slice(0, 10);
}

function addDays(iso: string, days: number): string {
  const d = new Date(iso + 'T00:00:00Z');
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

// Same half-open semantics as the backend: folds live in [start, end+1day),
// every boundary is anchored on the ORIGINAL start date, and the stored
// isEnd/oosEnd are the inclusive evaluated dates (OOS starts the day after
// IS ends — no shared candle, no gap).
function foldsFor(req: WalkForwardRequest): { isStart: string; isEnd: string; oosStart: string; oosEnd: string }[] {
  const step = req.stepMonths || req.oosMonths;
  const endExcl = addDays(req.endDate, 1);
  const out = [];
  for (let i = 0; ; i++) {
    const isStart = addMonths(req.startDate, i * step);
    const isEndEx = addMonths(req.startDate, i * step + req.inSampleMonths);
    const oosEndEx = addMonths(req.startDate, i * step + req.inSampleMonths + req.oosMonths);
    if (oosEndEx > endExcl) break;
    out.push({ isStart, isEnd: addDays(isEndEx, -1), oosStart: isEndEx, oosEnd: addDays(oosEndEx, -1) });
  }
  return out;
}

export function executeWalkForward(req: WalkForwardRequest): WalkForward {
  const id = `wf-${++counter}`;
  const now = new Date().toISOString();
  const folds: WalkForwardFold[] = [];
  let failures = 0;
  for (const [fi, w] of foldsFor(req).entries()) {
    for (const leg of req.legs) {
      const fold: WalkForwardFold = {
        foldIndex: fi, strategyId: leg.strategyId,
        isStart: w.isStart, isEnd: w.isEnd, oosStart: w.oosStart, oosEnd: w.oosEnd,
        status: 'completed',
      };
      for (const w2 of [
        { key: 'isResult' as const, start: w.isStart, end: w.isEnd, sample: 'in_sample' as const },
        { key: 'oosResult' as const, start: w.oosStart, end: w.oosEnd, sample: 'out_of_sample' as const },
      ]) {
        const run = executeBacktest({
          assetId: req.assetId, strategyId: leg.strategyId, timeframe: req.timeframe,
          startDate: w2.start, endDate: w2.end,
          initialCapital: req.initialCapital, positionSize: req.positionSize,
          commission: req.commission, slippage: req.slippage, parameters: leg.parameters,
        });
        const detail = getRunDetail(run.id);
        fold[w2.key] = { run, sample: w2.sample, metrics: detail?.metrics ?? null, equity: detail?.equity ?? [] };
        if (run.status !== 'completed') { fold.status = 'failed'; failures++; }
      }
      folds.push(fold);
    }
  }
  const summaries: Record<string, WalkForwardSummary> = {};
  for (const leg of req.legs) {
    const oos = folds.filter((f) => f.strategyId === leg.strategyId && f.oosResult?.metrics);
    const rets = oos.map((f) => f.oosResult!.metrics!.returnPct).sort((a, b) => a - b);
    const low = oos.filter((f) => f.oosResult!.metrics!.totalTrades < 30).length;
    summaries[leg.strategyId] = {
      completedFolds: oos.length,
      failedFolds: folds.filter((f) => f.strategyId === leg.strategyId && f.status === 'failed').length,
      positiveFolds: oos.filter((f) => f.oosResult!.metrics!.netPnl > 0).length,
      medianReturnPct: rets.length ? (rets.length % 2 ? rets[(rets.length - 1) / 2] : (rets[rets.length / 2 - 1] + rets[rets.length / 2]) / 2) : 0,
      worstReturnPct: rets.length ? rets[0] : 0,
      worstMaxDrawdown: Math.max(0, ...oos.map((f) => f.oosResult!.metrics!.maxDrawdown)),
      totalTrades: oos.reduce((a, f) => a + f.oosResult!.metrics!.totalTrades, 0),
      lowTradeFolds: low,
      inconclusive: oos.length === 0 || low * 2 > oos.length,
    };
  }
  const wf: WalkForward = {
    id, status: failures === 0 ? 'completed' : 'partial_failed',
    assetId: req.assetId, timeframe: req.timeframe,
    startDate: req.startDate, endDate: req.endDate,
    inSampleMonths: req.inSampleMonths, oosMonths: req.oosMonths, stepMonths: req.stepMonths || req.oosMonths,
    initialCapital: req.initialCapital, positionSize: req.positionSize,
    commission: req.commission, slippage: req.slippage,
    strategyIds: req.legs.map((l) => l.strategyId),
    error: null, createdAt: now, completedAt: now, folds, summaries,
  };
  registry.set(id, wf);
  return wf;
}

export function previewWalkForward(req: WalkForwardRequest): WalkForward {
  const wf = executeWalkForward(req); // mock: preview = folds without runs
  registry.delete(wf.id);
  return { ...wf, status: 'pending', folds: (wf.folds ?? []).map((f) => ({ ...f, status: 'pending' as const, isResult: undefined, oosResult: undefined })), summaries: undefined };
}

export function listWalkForwards(): WalkForward[] {
  return [...registry.values()].sort((a, b) => (b.createdAt ?? '').localeCompare(a.createdAt ?? ''));
}

export function getWalkForward(id: string): WalkForward | undefined {
  return registry.get(id);
}

export const mockWalkForwardService: WalkForwardService = {
  preview: (r) => Promise.resolve(previewWalkForward(r)),
  run: (r) => Promise.resolve(executeWalkForward(r)),
  list: () => Promise.resolve(listWalkForwards()),
  get: (id) => {
    const w = getWalkForward(id);
    return w ? Promise.resolve(w) : Promise.reject(new Error(`walk-forward ${id} not found`));
  },
};
