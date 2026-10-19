import { describe, expect, it } from 'vitest';
import { generateCandleSeries } from './candles';
import { signalsForStrategy } from './signals';
import { getRunDetail, listRuns } from './backtests';
import { mulberry32, hashSeed } from './seed';

describe('seeded generators', () => {
  it('produces deterministic random sequences', () => {
    const a = mulberry32(hashSeed('same'));
    const b = mulberry32(hashSeed('same'));
    expect([a(), a(), a()]).toEqual([b(), b(), b()]);
    expect(hashSeed('x')).not.toBe(hashSeed('y'));
  });

  it('generates identical candle series on repeated calls', () => {
    const s1 = generateCandleSeries('xauusd', '1h');
    const s2 = generateCandleSeries('xauusd', '1h');
    expect(s1.candles).toEqual(s2.candles);
    expect(s1.overlays.ema20).toEqual(s2.overlays.ema20);
  });

  it('produces well-formed candles (high >= low, ohlc consistent)', () => {
    const { candles } = generateCandleSeries('xauusd', '15m');
    expect(candles.length).toBeGreaterThan(100);
    for (const c of candles) {
      expect(c.high).toBeGreaterThanOrEqual(c.low);
      expect(c.high).toBeGreaterThanOrEqual(Math.max(c.open, c.close));
      expect(c.low).toBeLessThanOrEqual(Math.min(c.open, c.close));
      expect(c.volume).toBeGreaterThan(0);
    }
  });

  it('never generates candles past the fixed historical window', () => {
    const { candles } = generateCandleSeries('xauusd', '1h');
    const last = candles[candles.length - 1].time;
    expect(Date.parse(last)).toBeLessThanOrEqual(Date.parse('2024-12-31T00:00:00.000Z'));
  });

  it('EMA overlays start after their warmup periods', () => {
    const s = generateCandleSeries('xauusd', '1h');
    expect(s.overlays.ema20.length).toBe(s.candles.length - 19);
    expect(s.overlays.ema50.length).toBe(s.candles.length - 49);
  });

  it('generates deterministic signals and run details', () => {
    expect(signalsForStrategy('ema-cross')).toEqual(signalsForStrategy('ema-cross'));
    const d1 = getRunDetail('run-001');
    const d2 = getRunDetail('run-001');
    expect(d1?.equity).toEqual(d2?.equity);
    expect(d1?.metrics).toEqual(d2?.metrics);
  });

  it('seeds a mix of run outcomes including a failed run', () => {
    const runs = listRuns();
    expect(runs.length).toBeGreaterThanOrEqual(8);
    expect(runs.some((r) => r.status === 'failed')).toBe(true);
    expect(runs.some((r) => r.status === 'completed')).toBe(true);
    const failed = runs.find((r) => r.status === 'failed')!;
    const detail = getRunDetail(failed.id);
    expect(detail?.metrics).toBeNull();
    expect(detail?.run.error).toBeTruthy();
  });
});
