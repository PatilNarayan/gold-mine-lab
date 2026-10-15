import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { services } from '@/services';
import type { ComparisonRequest } from '@/types/domain';

export function useComparisons() {
  return useQuery({
    queryKey: ['comparisons'],
    queryFn: () => services.comparison.list(),
    staleTime: 15_000,
  });
}

export function useComparison(id: string | undefined) {
  return useQuery({
    queryKey: ['comparison', id],
    queryFn: () => services.comparison.get(id!),
    enabled: Boolean(id),
    staleTime: 60_000,
    retry: false,
  });
}

export function useRunComparison() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: ComparisonRequest) => services.comparison.run(req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['comparisons'] });
      qc.invalidateQueries({ queryKey: ['backtestRuns'] });
    },
  });
}
