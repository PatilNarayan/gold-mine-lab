import { useQuery } from '@tanstack/react-query';
import { services } from '@/services';
import { dataMode } from '@/lib/config';

/** Polls GET /api/health in api mode; resolves instantly in mock mode. */
export function useHealth() {
  return useQuery({
    queryKey: ['health'],
    queryFn: () => services.health.check(),
    refetchInterval: 15_000,
    retry: false,
    staleTime: 10_000,
    enabled: true,
    placeholderData:
      dataMode === 'mock'
        ? { status: 'ok', service: 'goldmine-lab (mock)', database: 'n/a — mock data' }
        : undefined,
  });
}
