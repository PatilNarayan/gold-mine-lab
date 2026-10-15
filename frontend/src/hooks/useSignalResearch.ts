import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { services } from '@/services';
import type { SignalResearchRequest } from '@/types/domain';

export function useSignalResearchList() {
  return useQuery({ queryKey: ['signalResearch'], queryFn: () => services.signalResearch.list(), staleTime: 15_000 });
}

export function useSignalResearch(id: string | undefined) {
  return useQuery({
    queryKey: ['signalResearch', id],
    queryFn: () => services.signalResearch.get(id!),
    enabled: Boolean(id),
    retry: false,
  });
}

export function useRunSignalResearch() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: SignalResearchRequest) => services.signalResearch.run(req),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: ['signalResearch'] }); },
  });
}
