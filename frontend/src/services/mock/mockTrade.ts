import type { PagedTrades, TradeFilters } from '@/types/domain';
import type { TradeService } from '@/services/interfaces';
import { allTrades } from '@/mock/backtests';
import { applyTradeFilters, paginate } from '@/lib/tradeFilters';
import { simulateDelay, isEmptyScenario } from './scenario';

export const mockTradeService: TradeService = {
  async getTrades(filters: TradeFilters): Promise<PagedTrades> {
    await simulateDelay();
    if (isEmptyScenario()) {
      return { items: [], total: 0, page: filters.page, pageSize: filters.pageSize };
    }
    const filtered = applyTradeFilters(allTrades(), filters);
    return {
      items: paginate(filtered, filters.page, filters.pageSize),
      total: filtered.length,
      page: filters.page,
      pageSize: filters.pageSize,
    };
  },
};
