import type {
  SignalResearch, SignalResearchRequest, SignalCandidateStats,
  SignalPairAgreement, SignalForwardResult,
} from '@/types/domain';
import type { SignalResearchService } from '../interfaces';

// Deterministic synthetic analysis for UI development — shape-parity only,
// never a real replay.
const items = new Map<string, SignalResearch>();
let counter = 0;

function days(a: string, b: string): number {
  return Math.max(1, Math.round((Date.parse(b) - Date.parse(a)) / 86_400_000));
}

const SHARES = [0.5, 0.25, 0.25, 0];
function regimeBuckets(names: string[], evalBars: number, buy: number, sell: number) {
  return names.map((r, i) => {
    const eb = i === names.length - 1 ? evalBars - Math.round(evalBars * (SHARES[0] + SHARES[1] + SHARES[2])) : Math.round(evalBars * SHARES[i]);
    const b = Math.round(buy * SHARES[i]), s = Math.round(sell * SHARES[i]);
    return {
      regime: r, evaluatedBars: eb, buy: b, sell: s, neutral: eb - b - s,
      actionablePct: eb > 0 ? +(((b + s) / eb) * 100).toFixed(1) : 0,
      buyEpisodes: Math.max(0, Math.round(b * 0.6)), sellEpisodes: Math.max(0, Math.round(s * 0.6)),
      avgScore: b + s > 0 ? 0.4 : null,
    };
  });
}
const trendBuckets = (e: number, b: number, s: number) => regimeBuckets(['uptrend', 'downtrend', 'transition', 'unknown'], e, b, s);

// Synthetic benchmark cell: small event counts → insufficient_data so the
// unavailable state is exercised in mock mode.
function bench(h: number, events: number, em: number, emd: number, ep: number, worst: number, regime: boolean) {
  const ctl = regime ? 120 : 600, baseMean = 0.08 + h * 0.02;
  const thin = events < 10;
  return {
    eventCount: events, controlCount: ctl, baselineCount: events + ctl,
    eventMean: events > 0 ? em : null, eventMedian: events > 0 ? emd : null,
    baselineMean: baseMean, baselineMedian: baseMean - 0.03,
    diffMean: events > 0 ? +(em - baseMean).toFixed(2) : null,
    diffMedian: events > 0 ? +(emd - baseMean).toFixed(2) : null,
    eventPositivePct: events > 0 ? ep : null, baselinePositivePct: 53,
    worstEventReturn: events > 0 ? worst : null,
    ciStatus: thin ? 'insufficient_data' : 'inconclusive',
    ciLow: thin ? null : -0.2, ciHigh: thin ? null : 0.9,
    validReplicates: thin ? 0 : 1000, blockLength: h + 1,
  };
}
const volBuckets = (e: number, b: number, s: number) => regimeBuckets(['low', 'normal', 'high', 'unknown'], e, b, s);

function synth(req: SignalResearchRequest): SignalResearch {
  const evalBars = days(req.startDate, req.endDate) + 1;
  const candidates: SignalCandidateStats[] = req.legs.map((l, i) => {
    const buy = Math.round(evalBars * (0.01 + i * 0.008));
    const sell = Math.round(evalBars * (0.008 + i * 0.006));
    return {
      strategyId: l.strategyId, version: '1.0.0', evaluatedBars: evalBars,
      buy, sell, neutral: evalBars - buy - sell,
      actionablePct: +(((buy + sell) / evalBars) * 100).toFixed(1),
      buyEpisodes: Math.max(1, Math.round(buy * 0.6)),
      sellEpisodes: Math.max(1, Math.round(sell * 0.6)),
      avgScore: 0.42,
      ...(req.regimeAnalysis ? { byTrend: trendBuckets(evalBars, buy, sell), byVolatility: volBuckets(evalBars, buy, sell) } : {}),
    };
  });
  const pairs: SignalPairAgreement[] = [];
  for (let i = 0; i < req.legs.length; i++) {
    for (let j = i + 1; j < req.legs.length; j++) {
      const co = Math.round(evalBars * 0.01);
      pairs.push({
        a: req.legs[i].strategyId, b: req.legs[j].strategyId,
        bothBuy: co - 1, bothSell: 1, sameDirection: co, opposite: 0,
        oneActionable: Math.round(evalBars * 0.03),
        bothNeutral: evalBars - co - Math.round(evalBars * 0.03),
        coActionable: co,
        agreementPct: 100, conflictPct: 0,
        ...(req.regimeAnalysis ? {
          byTrend: ['uptrend', 'downtrend', 'transition', 'unknown'].map((r, k) => ({
            regime: r, bothBuy: k === 0 ? co - 1 : 0, bothSell: k === 0 ? 1 : 0,
            sameDirection: k === 0 ? co : 0, opposite: 0,
            oneActionable: Math.round(evalBars * 0.008), bothNeutral: 0, coActionable: k === 0 ? co : 0,
            agreementPct: k === 0 ? 100 : null, conflictPct: k === 0 ? 0 : null,
          })),
          byVolatility: ['low', 'normal', 'high', 'unknown'].map((r) => ({
            regime: r, bothBuy: 0, bothSell: 0, sameDirection: 0, opposite: 0,
            oneActionable: 0, bothNeutral: 0, coActionable: 0,
            agreementPct: null, conflictPct: null,
          })),
        } : {}),
      });
    }
  }
  const forward: SignalForwardResult[] = req.eventStudy
    ? req.legs.map((l) => ({
        strategyId: l.strategyId,
        horizons: [1, 5, 10, 20].map((h) => ({
          horizon: h, strategyId: l.strategyId, eligible: 5, excluded: 0,
          meanReturn: 0.3, medianReturn: 0.2, positivePct: 60, worstReturn: -1.4,
          benchmark: bench(h, 5, 0.3, 0.2, 60, -1.4, false),
        })),
        ...(req.regimeAnalysis ? {
          byTrend: ['uptrend', 'downtrend', 'transition', 'unknown'].map((r, k) => ({
            regime: r, horizons: [1, 5, 10, 20].map((h) => ({
              horizon: h, strategyId: l.strategyId, eligible: k === 0 ? 3 : 0, excluded: 0,
              meanReturn: k === 0 ? 0.4 : null, medianReturn: k === 0 ? 0.3 : null,
              positivePct: k === 0 ? 66 : null, worstReturn: k === 0 ? -1.1 : null,
              benchmark: bench(h, k === 0 ? 3 : 0, 0.4, 0.3, 66, -1.1, true),
            })),
          })),
          byVolatility: ['low', 'normal', 'high', 'unknown'].map((r, k) => ({
            regime: r, horizons: [1, 5, 10, 20].map((h) => ({
              horizon: h, strategyId: l.strategyId, eligible: k === 1 ? 2 : 0, excluded: 0,
              meanReturn: k === 1 ? 0.2 : null, medianReturn: k === 1 ? 0.1 : null,
              positivePct: k === 1 ? 50 : null, worstReturn: k === 1 ? -0.8 : null,
              benchmark: bench(h, k === 1 ? 2 : 0, 0.2, 0.1, 50, -0.8, true),
            })),
          })),
        } : {}),
      }))
    : [];
  const id = `mock-sr-${++counter}`;
  const r: SignalResearch = {
    id, status: 'completed', name: req.name ?? '',
    assetId: req.assetId, timeframe: req.timeframe,
    startDate: req.startDate, endDate: req.endDate,
    eventStudy: Boolean(req.eventStudy),
    dataset: 'mock:dataset@sha256:deadbeef', lastCandle: `${req.endDate}T00:00:00Z`,
    evaluatedBars: evalBars, warmupBars: 44,
    strategyIds: req.legs.map((l) => l.strategyId),
    candidates, pairs, forward,
    regimeVersion: req.regimeAnalysis ? 'regime-v1' : null,
    benchmarkMeta: req.eventStudy ? {
      eventStudyVersion: 'eventstudy-v1', baselineVersion: 'baseline-v1',
      bootstrapVersion: 'mbb-v1', blockLengthRule: 'horizon+1',
      replicates: 1000, seed: 6221073,
      minEvents: 10, minControls: 50, minReplicateRate: 0.8,
    } : null,
    error: null,
    createdAt: new Date().toISOString(),
  };
  items.set(id, r);
  return r;
}

export const mockSignalResearchService: SignalResearchService = {
  async run(request: SignalResearchRequest): Promise<SignalResearch> {
    if (request.legs.length < 2) {
      const { ApiError } = await import('@/types/api');
      throw new ApiError('invalid_request', 'at least 2 candidates are required', 'mock');
    }
    return synth(request);
  },
  list(): Promise<SignalResearch[]> {
    return Promise.resolve([...items.values()].reverse());
  },
  get(id: string): Promise<SignalResearch> {
    const r = items.get(id);
    if (!r) {
      return Promise.reject(new Error('signal research not found'));
    }
    return Promise.resolve(r);
  },
};
