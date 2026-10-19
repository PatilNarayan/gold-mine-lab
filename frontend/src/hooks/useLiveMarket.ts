import { useQuery } from '@tanstack/react-query';
import { services } from '@/services';
import type { Timeframe } from '@/types/domain';

/**
 * Live-market queries poll the BACKEND's cached provider state only — the
 * frontend never talks to Twelve Data. Intervals are bounded and queries are
 * disabled when the live view is not active.
 */
export function useLiveStatus(enabled = true) {
  return useQuery({
    queryKey: ['liveStatus'],
    queryFn: () => services.live.getStatus(),
    refetchInterval: 10_000,
    enabled,
  });
}

export function useLiveQuote(assetId: string, enabled = true) {
  return useQuery({
    queryKey: ['liveQuote', assetId],
    queryFn: () => services.live.getQuote(assetId),
    refetchInterval: 5_000,
    enabled,
    retry: 1,
  });
}

export function useLiveCandles(assetId: string, timeframe: Timeframe, enabled = true, limit = 200) {
  return useQuery({
    queryKey: ['liveCandles', assetId, timeframe, limit],
    queryFn: () => services.live.getCandles(assetId, timeframe, limit),
    refetchInterval: 10_000,
    enabled,
    retry: 1,
  });
}
