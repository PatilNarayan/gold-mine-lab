import type { PagedTrades, TradeFilters } from '@/types/domain';
import { pagedTradesSchema } from '@/types/domain';
import type { TradeService } from '@/services/interfaces';
import { apiClient } from './client';

export const httpTradeService: TradeService = {
  getTrades(filters: TradeFilters): Promise<PagedTrades> {
    const params = new URLSearchParams();
    if (filters.strategyId) params.set('strategy', filters.strategyId);
    if (filters.side) params.set('side', filters.side);
    if (filters.result) params.set('result', filters.result);
    if (filters.from) params.set('from', filters.from);
    if (filters.to) params.set('to', filters.to);
    params.set('page', String(filters.page));
    params.set('pageSize', String(filters.pageSize));
    return apiClient.get(`/api/trades?${params.toString()}`, pagedTradesSchema);
  },
};
