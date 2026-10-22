import { describe, it, expect } from 'vitest';
import { mergeLiveBar } from './liveBars';
import type { LiveBarUpdate, LiveCandles } from '@/types/domain';

const base: LiveCandles = {
  assetId: 'xauusd',
  provider: 'tiingo',
  symbol: 'xauusd',
  timeframe: '15m',
  status: 'connected',
  candles: [
    { time: '2026-10-22T18:00:00Z', open: 4046, high: 4047, low: 4036, close: 4036.2, volume: null },
    { time: '2026-10-22T18:15:00Z', open: 4036.2, high: 4037, low: 4036, close: 4036.75, volume: null },
  ],
  overlays: {
    ema20: [{ time: '2026-10-22T18:15:00Z', value: 4040.1 }],
    ema50: [{ time: '2026-10-22T18:15:00Z', value: 4042.5 }],
  },
};

const upd = (candle: Partial<LiveCandles['candles'][0]> & { time: string }, ema20?: number, ema50?: number): LiveBarUpdate => ({
  timeframe: '15m',
  candle: { open: 0, high: 0, low: 0, close: 0, volume: null, ...candle },
  ema20: ema20 !== undefined ? { time: candle.time, value: ema20 } : undefined,
  ema50: ema50 !== undefined ? { time: candle.time, value: ema50 } : undefined,
});

describe('mergeLiveBar', () => {
  it('updates the forming bar in place on same-bucket ticks', () => {
    const out = mergeLiveBar(base, upd({ time: '2026-10-22T18:15:00Z', high: 4039, close: 4038.9 }, 4040.2, 4042.4))!;
    expect(out.candles).toHaveLength(2);
    expect(out.candles[1].close).toBe(4038.9);
    expect(out.candles[1].high).toBe(4039);
    expect(out.overlays.ema20).toHaveLength(1);
    expect(out.overlays.ema20[0].value).toBe(4040.2);
  });

  it('closes the previous bucket and appends on rollover', () => {
    const rolled = mergeLiveBar(base, upd({ time: '2026-10-22T18:30:00Z', open: 4038, high: 4038, low: 4038, close: 4038 }, 4040.5))!;
    expect(rolled.candles).toHaveLength(3);
    expect(rolled.candles[1].close).toBe(4036.75); // closed bar unchanged
    expect(rolled.candles[2].open).toBe(4038);
    expect(rolled.overlays.ema20).toHaveLength(2);
    expect(rolled.overlays.ema20[1]).toEqual({ time: '2026-10-22T18:30:00Z', value: 4040.5 });
  });

  it('ignores out-of-order and duplicate updates', () => {
    const out = mergeLiveBar(base, upd({ time: '2026-10-22T18:00:00Z', close: 1 }))!;
    expect(out.candles[0].close).toBe(4036.2);
    // duplicate of last bucket still replaces in place — idempotent
    const dup = mergeLiveBar(base, upd({ time: '2026-10-22T18:15:00Z', close: 4036.75 }))!;
    expect(dup.candles).toHaveLength(2);
    expect(dup.candles[1].close).toBe(4036.75);
  });

  it('ignores updates for other timeframes and missing cache', () => {
    const wrong: LiveBarUpdate = { timeframe: '1h', candle: upd({ time: '2026-10-22T18:15:00Z' }).candle };
    expect(mergeLiveBar(base, wrong)).toBe(base);
    expect(mergeLiveBar(undefined, upd({ time: 'x' }))).toBeUndefined();
  });

  it('trims the window from the front', () => {
    const full = { ...base, candles: Array.from({ length: 200 }, (_, i) => ({
      time: new Date(1761000000000 + i * 60000).toISOString(), open: 1, high: 2, low: 0.5, close: 1.5, volume: null,
    })) };
    const next = mergeLiveBar(full, upd({ time: new Date(1761000000000 + 200 * 60000).toISOString(), close: 9 }))!;
    expect(next.candles).toHaveLength(200);
    expect(next.candles[199].close).toBe(9);
    expect(next.candles[0].time).toBe(full.candles[1].time);
  });
});
