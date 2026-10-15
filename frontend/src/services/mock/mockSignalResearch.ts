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
      });
    }
  }
  const forward: SignalForwardResult[] = req.eventStudy
    ? req.legs.map((l) => ({
        strategyId: l.strategyId,
        horizons: [1, 5, 10, 20].map((h) => ({
          horizon: h, strategyId: l.strategyId, eligible: 5, excluded: 0,
          meanReturn: 0.3, medianReturn: 0.2, positivePct: 60, worstReturn: -1.4,
        })),
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
    candidates, pairs, forward, error: null,
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
