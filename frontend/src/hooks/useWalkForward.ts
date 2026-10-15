import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { services } from '@/services';
import type { WalkForwardRequest } from '@/types/domain';

export function useWalkForwards() {
  return useQuery({
    queryKey: ['walkforwards'],
    queryFn: () => services.walkForward.list(),
    staleTime: 15_000,
  });
}

export function useWalkForward(id: string | undefined) {
  return useQuery({
    queryKey: ['walkforward', id],
    queryFn: () => services.walkForward.get(id!),
    enabled: Boolean(id),
    staleTime: 60_000,
    retry: false,
  });
}

export function usePreviewWalkForward() {
  return useMutation({
    mutationFn: (req: WalkForwardRequest) => services.walkForward.preview(req),
  });
}

export function useRunWalkForward() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: WalkForwardRequest) => services.walkForward.run(req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['walkforwards'] });
      qc.invalidateQueries({ queryKey: ['backtestRuns'] });
    },
  });
}
