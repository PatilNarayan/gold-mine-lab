import type { BacktestTrade, TradeFilters } from '@/types/domain';
import { tradeSideSchema, tradeResultSchema } from '@/types/domain';

/**
 * Pure trade-filtering logic shared by the mock TradeService and unit tests.
 * The page reads filter state from URL query params so views are shareable.
 */
export function applyTradeFilters(trades: BacktestTrade[], filters: TradeFilters): BacktestTrade[] {
  return trades.filter((t) => {
    if (filters.strategyId && t.strategyId !== filters.strategyId) return false;
    if (filters.side && t.side !== filters.side) return false;
    if (filters.result && t.result !== filters.result) return false;
    if (filters.from && t.entryTime < filters.from) return false;
    if (filters.to && t.entryTime > filters.to + 'T23:59:59.999Z') return false;
    return true;
  });
}

export function paginate<T>(items: T[], page: number, pageSize: number): T[] {
  const start = (page - 1) * pageSize;
  return items.slice(start, start + pageSize);
}

/** Parse URL search params into typed TradeFilters. */
export function tradeFiltersFromSearchParams(params: URLSearchParams): TradeFilters {
  const side = tradeSideSchema.safeParse(params.get('side') ?? undefined);
  const result = tradeResultSchema.safeParse(params.get('result') ?? undefined);
  const page = Number(params.get('page') ?? '1');
  return {
    strategyId: params.get('strategy') || undefined,
    side: side.success ? side.data : undefined,
    result: result.success ? result.data : undefined,
    from: params.get('from') || undefined,
    to: params.get('to') || undefined,
    page: Number.isInteger(page) && page >= 1 ? page : 1,
    pageSize: 25,
  };
}
