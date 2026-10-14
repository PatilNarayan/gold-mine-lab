import type { Candle, CandleSeries, SeriesPoint, Timeframe } from '@/types/domain';
import { TIMEFRAME_MS } from '@/lib/constants';
import { assetById, assetMeta } from './assets';
import { rngFor, randBetween, round } from './seed';

/**
 * Fixed historical window — mock candles always end at a past date and never
 * imply "now". Synthetic data only.
 */
export const HISTORY_END = '2024-12-31T00:00:00.000Z';
export const HISTORY_START = '2024-01-02T00:00:00.000Z';

export const DEFAULT_CANDLE_COUNT = 240;

function ema(values: number[], period: number): (number | null)[] {
  const k = 2 / (period + 1);
  const out: (number | null)[] = new Array(values.length).fill(null);
  let prev: number | null = null;
  for (let i = 0; i < values.length; i++) {
    if (i < period - 1) continue;
    if (prev === null) {
      let sum = 0;
      for (let j = i - period + 1; j <= i; j++) sum += values[j];
      prev = sum / period;
    } else {
      prev = values[i] * k + prev * (1 - k);
    }
    out[i] = prev;
  }
  return out;
}

const cache = new Map<string, CandleSeries>();

/** Deterministic candle series with precomputed EMA20/EMA50 overlays. */
export function generateCandleSeries(
  assetId: string,
  timeframe: Timeframe,
  count = DEFAULT_CANDLE_COUNT,
): CandleSeries {
  const key = `${assetId}|${timeframe}|${count}`;
  const hit = cache.get(key);
  if (hit) return hit;

  const meta = assetMeta(assetId);
  const asset = assetById(assetId);
  const rng = rngFor(`candles|${key}`);
  const step = TIMEFRAME_MS[timeframe];
  const endMs = Date.parse(HISTORY_END);
  const digits = meta.digits;

  // Deterministic drift: some assets trend up, some down, some chop.
  const drift = randBetween(rng, -0.0006, 0.0011);
  const vol = meta.volatility;

  const candles: Candle[] = [];
  let prevClose = meta.basePrice * (1 + randBetween(rng, -0.03, 0.03));
  for (let i = 0; i < count; i++) {
    const t = endMs - (count - 1 - i) * step;
    const shock = (rng() - 0.5) * 2 * vol;
    const open = prevClose;
    const close = open * (1 + drift + shock);
    const wickUp = Math.abs(shock) * 0.8 + rng() * vol * 0.6;
    const wickDn = Math.abs(shock) * 0.8 + rng() * vol * 0.6;
    const high = Math.max(open, close) * (1 + wickUp * 0.5);
    const low = Math.min(open, close) * (1 - wickDn * 0.5);
    const volume = Math.round(meta.baseVolume * randBetween(rng, 0.35, 1.8));
    candles.push({
      time: new Date(t).toISOString(),
      open: round(open, digits),
      high: round(high, digits),
      low: round(low, digits),
      close: round(close, digits),
      volume,
    });
    prevClose = close;
  }

  const closes = candles.map((c) => c.close);
  const toPoints = (arr: (number | null)[]): SeriesPoint[] =>
    arr
      .map((v, i) => (v === null ? null : { time: candles[i].time, value: round(v, digits) }))
      .filter((p): p is SeriesPoint => p !== null);

  const series: CandleSeries = {
    assetId: asset?.id ?? assetId,
    timeframe,
    candles,
    overlays: {
      ema20: toPoints(ema(closes, 20)),
      ema50: toPoints(ema(closes, 50)),
    },
  };
  cache.set(key, series);
  return series;
}
