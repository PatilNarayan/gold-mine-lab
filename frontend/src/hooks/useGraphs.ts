import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { services } from '@/services';
import type { CreateGraphRequest, GraphBacktestRequest, GraphDefinition } from '@/types/domain';

export function useGraphs() {
  return useQuery({ queryKey: ['graphs'], queryFn: () => services.graph.list(), staleTime: 15_000 });
}

export function useGraph(id: string | undefined) {
  return useQuery({ queryKey: ['graph', id], queryFn: () => services.graph.get(id!), enabled: Boolean(id), retry: false });
}

export function useGraphRevisions(id: string | undefined) {
  return useQuery({ queryKey: ['graphRevisions', id], queryFn: () => services.graph.listRevisions(id!), enabled: Boolean(id), retry: false });
}

export function useCreateGraph() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateGraphRequest) => services.graph.create(req),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['graphs'] }),
  });
}

export function useCreateRevision(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (def: GraphDefinition) => services.graph.createRevision(id, def),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['graph', id] });
      qc.invalidateQueries({ queryKey: ['graphRevisions', id] });
      qc.invalidateQueries({ queryKey: ['graphs'] });
    },
  });
}

export function useValidateGraph() {
  return useMutation({ mutationFn: (def: GraphDefinition) => services.graph.validate(def) });
}

export function useRunGraphBacktest(id: string, revision: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: GraphBacktestRequest) => services.graph.runBacktest(id, revision, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['backtestRuns'] }),
  });
}
