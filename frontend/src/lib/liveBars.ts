import type { LiveBarUpdate, LiveCandle, LiveCandles } from '@/types/domain';

/**
 * Folds one streamed forming-bar update into a cached live-candles window.
 *
 * The backend sends the CURRENT bucket only: same timestamp → replace the
 * last bar in place; newer timestamp → the previous forming bar is closed and
 * the new bucket appends. Older or duplicate updates are ignored — events are
 * idempotent so reconnects never double-apply a tick. Bars beyond the window
 * are trimmed from the front. Weekend/session gaps surface as missing
 * buckets, never fabricated bars.
 */
export function mergeLiveBar(prev: LiveCandles | undefined, u: LiveBarUpdate, maxBars = 200): LiveCandles | undefined {
  if (!prev || prev.timeframe !== u.timeframe || !u.candle) return prev;
  return {
    ...prev,
    candles: trimTail(mergeTail(prev.candles, u.candle), maxBars),
    overlays: {
      ema20: mergeTail(prev.overlays.ema20, u.ema20),
      ema50: mergeTail(prev.overlays.ema50, u.ema50),
    },
  };
}

// mergeTail replaces the last element when `item` shares its timestamp,
// appends when newer, and ignores older/duplicate items.
function mergeTail<T extends { time: string }>(list: T[], item: T | undefined): T[] {
  if (!item) return list;
  const last = list[list.length - 1];
  if (!last || item.time > last.time) return [...list, item];
  if (item.time === last.time) return [...list.slice(0, -1), item];
  return list;
}

function trimTail(list: LiveCandle[], max: number): LiveCandle[] {
  return list.length > max ? list.slice(list.length - max) : list;
}
