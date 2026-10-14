import type { Signal } from '@/types/domain';
import { generateCandleSeries } from './candles';
import { MOCK_STRATEGIES, strategyById } from './strategies';
import { rngFor, randBetween, round, pick } from './seed';
import { assetMeta } from './assets';

const NEUTRAL_REASONS = [
  'No cross within threshold band',
  'Signal strength below cutoff',
  'Conflicting timeframe momentum',
  'Volatility filter suppressed entry',
];

/**
 * Signals derived in the mock layer from the candle/EMA series — the same
 * shape the backend will emit over REST/WebSocket. Fully deterministic.
 */
const cache = new Map<string, Signal[]>();

export function signalsForStrategy(strategyId: string): Signal[] {
  const cached = cache.get(strategyId);
  if (cached) return cached;

  const strategy = strategyById(strategyId);
  if (!strategy) return [];

  const rng = rngFor(`signals|${strategyId}`);
  const signals: Signal[] = [];

  for (const assetId of strategy.supportedAssets) {
    const meta = assetMeta(assetId);
    const series = generateCandleSeries(assetId, strategy.timeframe);
    const e20 = new Map(series.overlays.ema20.map((p) => [p.time, p.value]));
    const e50 = new Map(series.overlays.ema50.map((p) => [p.time, p.value]));

    let prevDiff = 0;
    let n = 0;
    for (const c of series.candles) {
      const a = e20.get(c.time);
      const b = e50.get(c.time);
      if (a === undefined || b === undefined) continue;
      const diff = a - b;
      let action: Signal['action'] | null = null;
      let reason = '';
      if (prevDiff <= 0 && diff > 0) {
        action = 'BUY';
        reason = 'EMA20 crossed above EMA50';
      } else if (prevDiff >= 0 && diff < 0) {
        action = 'SELL';
        reason = 'EMA20 crossed below EMA50';
      } else if (rng() < 0.025) {
        action = 'NEUTRAL';
        reason = pick(rng, NEUTRAL_REASONS);
      }
      if (action) {
        signals.push({
          id: `sig-${strategyId}-${assetId}-${n++}`,
          strategyId,
          assetId,
          action,
          score: round(randBetween(rng, 0.3, 0.95), 2),
          price: c.close,
          timestamp: c.time,
          reason,
          metadata: { ema20: round(a, meta.digits), ema50: round(b, meta.digits) },
        });
      }
      prevDiff = diff;
    }
  }

  signals.sort((a, b) => a.timestamp.localeCompare(b.timestamp));
  cache.set(strategyId, signals);
  return signals;
}

export function allSignals(): Signal[] {
  return MOCK_STRATEGIES.flatMap((s) => signalsForStrategy(s.id)).sort((a, b) =>
    b.timestamp.localeCompare(a.timestamp),
  );
}

export function latestSignalForStrategy(strategyId: string): Signal | null {
  const list = signalsForStrategy(strategyId);
  return list.length ? list[list.length - 1] : null;
}
