import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { services } from '@/services';
import type { TradeFilters } from '@/types/domain';

export function useTrades(filters: TradeFilters) {
  return useQuery({
    queryKey: [
      'trades',
      filters.strategyId ?? null,
      filters.side ?? null,
      filters.result ?? null,
      filters.from ?? null,
      filters.to ?? null,
      filters.page,
      filters.pageSize,
    ],
    queryFn: () => services.trade.getTrades(filters),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
  });
}
