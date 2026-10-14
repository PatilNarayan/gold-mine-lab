import { useQuery } from '@tanstack/react-query';
import { services } from '@/services';
import type { Timeframe } from '@/types/domain';

export function useAssets() {
  return useQuery({
    queryKey: ['assets'],
    queryFn: () => services.market.getAssets(),
    staleTime: 60_000,
  });
}

export function useCandles(assetId: string, timeframe: Timeframe) {
  return useQuery({
    queryKey: ['candles', assetId, timeframe],
    queryFn: () => services.market.getCandles(assetId, timeframe),
    staleTime: 30_000,
  });
}

export function useMarketSummary(assetId: string, timeframe: Timeframe) {
  return useQuery({
    queryKey: ['marketSummary', assetId, timeframe],
    queryFn: () => services.market.getMarketSummary(assetId, timeframe),
    staleTime: 30_000,
  });
}
