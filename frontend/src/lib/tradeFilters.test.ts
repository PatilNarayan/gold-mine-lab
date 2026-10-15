import { describe, expect, it } from 'vitest';
import { applyTradeFilters, paginate, tradeFiltersFromSearchParams } from './tradeFilters';
import type { BacktestTrade } from '@/types/domain';

const trade = (over: Partial<BacktestTrade>): BacktestTrade => ({
  id: 't1',
  runId: 'run-001',
  strategyId: 'ema-cross',
  assetId: 'xauusd',
  side: 'BUY',
  entryTime: '2024-06-10T10:00:00.000Z',
  entryPrice: 2300,
  exitTime: '2024-06-10T12:00:00.000Z',
  exitPrice: 2310,
  quantity: 1,
  pnl: 10,
  status: 'closed',
  result: 'win',
  ...over,
});

const trades = [
  trade({ id: 'a', strategyId: 'ema-cross', side: 'BUY', result: 'win' }),
  trade({ id: 'b', strategyId: 'rsi-mean-reversion', side: 'SELL', result: 'loss', pnl: -5 }),
  trade({ id: 'c', strategyId: 'ema-cross', status: 'open', result: 'open', exitTime: null, exitPrice: null, pnl: null }),
  trade({ id: 'd', strategyId: 'macd-trend', entryTime: '2024-01-05T00:00:00.000Z' }),
];

describe('applyTradeFilters', () => {
  const base = { page: 1, pageSize: 25 };

  it('filters by strategy', () => {
    const out = applyTradeFilters(trades, { ...base, strategyId: 'ema-cross' });
    expect(out.map((t) => t.id).sort()).toEqual(['a', 'c']);
  });

  it('filters by side', () => {
    const out = applyTradeFilters(trades, { ...base, side: 'SELL' });
    expect(out.map((t) => t.id)).toEqual(['b']);
  });

  it('filters by result', () => {
    expect(applyTradeFilters(trades, { ...base, result: 'open' }).map((t) => t.id)).toEqual(['c']);
    expect(applyTradeFilters(trades, { ...base, result: 'win' }).map((t) => t.id).sort()).toEqual(['a', 'd']);
  });

  it('filters by date range inclusively', () => {
    const out = applyTradeFilters(trades, { ...base, from: '2024-06-01', to: '2024-06-30' });
    expect(out.map((t) => t.id).sort()).toEqual(['a', 'b', 'c']);
  });
});

describe('tradeFiltersFromSearchParams', () => {
  it('parses query params and ignores invalid values', () => {
    const p = new URLSearchParams('strategy=ema-cross&side=BUY&result=win&page=2');
    expect(tradeFiltersFromSearchParams(p)).toEqual({
      strategyId: 'ema-cross',
      side: 'BUY',
      result: 'win',
      from: undefined,
      to: undefined,
      page: 2,
      pageSize: 25,
    });
    const bad = new URLSearchParams('side=WRONG&page=-3');
    const parsed = tradeFiltersFromSearchParams(bad);
    expect(parsed.side).toBeUndefined();
    expect(parsed.page).toBe(1);
  });
});

describe('paginate', () => {
  it('slices pages correctly', () => {
    const items = Array.from({ length: 60 }, (_, i) => i);
    expect(paginate(items, 1, 25)).toHaveLength(25);
    expect(paginate(items, 3, 25)).toHaveLength(10);
    expect(paginate(items, 4, 25)).toHaveLength(0);
  });
});
