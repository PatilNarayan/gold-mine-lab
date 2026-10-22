import { useQuery } from '@tanstack/react-query';
import { services } from '@/services';
import type { Timeframe } from '@/types/domain';

/**
 * Live-market queries bootstrap from the BACKEND's cached provider state —
 * the frontend never talks to the upstream provider. After bootstrap,
 * updates arrive over the WebSocket stream (useLiveStream) which writes
 * these same query keys; these hooks intentionally do NOT poll for ticks.
 * Status keeps a slow backstop interval so a silent stream drop still
 * surfaces honest connectivity state.
 */
export function useLiveStatus(enabled = true) {
  return useQuery({
    queryKey: ['liveStatus'],
    queryFn: () => services.live.getStatus(),
    refetchInterval: 15_000,
    enabled,
  });
}

export function useLiveQuote(assetId: string, enabled = true) {
  return useQuery({
    queryKey: ['liveQuote', assetId],
    queryFn: () => services.live.getQuote(assetId),
    enabled,
    retry: 1,
  });
}

export function useLiveCandles(assetId: string, timeframe: Timeframe, enabled = true, limit = 200) {
  return useQuery({
    queryKey: ['liveCandles', assetId, timeframe, limit],
    queryFn: () => services.live.getCandles(assetId, timeframe, limit),
    enabled,
    retry: 1,
  });
}
