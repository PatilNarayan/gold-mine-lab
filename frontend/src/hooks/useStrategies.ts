import { useQuery } from '@tanstack/react-query';
import { services } from '@/services';

export function useStrategies() {
  return useQuery({
    queryKey: ['strategies'],
    queryFn: () => services.strategy.list(),
    staleTime: 30_000,
  });
}

export function useStrategy(id: string | undefined) {
  return useQuery({
    queryKey: ['strategy', id],
    queryFn: () => services.strategy.get(id!),
    enabled: Boolean(id),
    staleTime: 60_000,
    retry: false,
  });
}

export function useSignals(opts?: { assetId?: string; strategyId?: string; limit?: number }) {
  return useQuery({
    queryKey: ['signals', opts?.assetId ?? null, opts?.strategyId ?? null, opts?.limit ?? null],
    queryFn: () => services.strategy.getSignals(opts),
    staleTime: 15_000,
  });
}
