import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { services } from '@/services';
import type { ResearchStudyRequest } from '@/types/domain';

export function useResearchStudies() {
  return useQuery({ queryKey: ['researchStudies'], queryFn: () => services.researchStudies.list(), staleTime: 15_000 });
}

export function useResearchStudy(id: string | undefined) {
  return useQuery({
    queryKey: ['researchStudies', id],
    queryFn: () => services.researchStudies.get(id!),
    enabled: Boolean(id),
    retry: false,
  });
}

export function useCreateResearchStudy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (request: ResearchStudyRequest) => services.researchStudies.create(request),
    onSuccess: (study) => {
      void qc.invalidateQueries({ queryKey: ['researchStudies'] });
      void qc.setQueryData(['researchStudies', study.id], study);
    },
  });
}

export function useUpdateResearchStudy(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (request: ResearchStudyRequest) => services.researchStudies.update(id, request),
    onSuccess: (study) => {
      void qc.invalidateQueries({ queryKey: ['researchStudies'] });
      void qc.setQueryData(['researchStudies', id], study);
    },
  });
}

function useStudyAction(action: (id: string) => Promise<unknown>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => action(id),
    onSuccess: (_result, id) => {
      void qc.invalidateQueries({ queryKey: ['researchStudies'] });
      void qc.invalidateQueries({ queryKey: ['researchStudies', id] });
    },
  });
}

export function useFreezeResearchStudy() {
  return useStudyAction((id) => services.researchStudies.freeze(id));
}
export function useRunStudyExploratory() {
  return useStudyAction((id) => services.researchStudies.runExploratory(id));
}
export function useValidateStudyHoldout() {
  return useStudyAction((id) => services.researchStudies.validateHoldout(id));
}
