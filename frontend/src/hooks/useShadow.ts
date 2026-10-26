import { useQuery } from '@tanstack/react-query';
import { services } from '@/services';

/**
 * Shadow-trading queries — simulated execution state. Polls modestly:
 * fills/decisions are candle-cadence events, not tick-rate data.
 */
export function useShadowStatus(enabled = true) {
  return useQuery({
    queryKey: ['shadowStatus'],
    queryFn: () => services.shadow.getStatus(),
    refetchInterval: 15_000,
    enabled,
    retry: 1,
  });
}

export function useShadowOrders(limit = 100, enabled = true) {
  return useQuery({
    queryKey: ['shadowOrders', limit],
    queryFn: () => services.shadow.getOrders(limit),
    refetchInterval: 15_000,
    enabled,
    retry: 1,
  });
}

export function useShadowDecisions(limit = 50, enabled = true) {
  return useQuery({
    queryKey: ['shadowDecisions', limit],
    queryFn: () => services.shadow.getDecisions(limit),
    refetchInterval: 15_000,
    enabled,
    retry: 1,
  });
}
