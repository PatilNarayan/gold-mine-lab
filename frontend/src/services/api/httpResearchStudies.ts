import { z } from 'zod';
import {
  researchStudyRequestSchema,
  researchStudySchema,
  type ResearchStudy,
  type ResearchStudyRequest,
} from '@/types/domain';
import type { ResearchStudyService } from '@/services/interfaces';
import { apiClient } from './client';

export const httpResearchStudyService: ResearchStudyService = {
  create(request: ResearchStudyRequest): Promise<ResearchStudy> {
    return apiClient.post('/api/research-studies', researchStudyRequestSchema.parse(request), researchStudySchema);
  },
  update(id: string, request: ResearchStudyRequest): Promise<ResearchStudy> {
    return apiClient.patch(`/api/research-studies/${encodeURIComponent(id)}`, researchStudyRequestSchema.parse(request), researchStudySchema);
  },
  list(): Promise<ResearchStudy[]> {
    return apiClient.get('/api/research-studies', z.array(researchStudySchema));
  },
  get(id: string): Promise<ResearchStudy> {
    return apiClient.get(`/api/research-studies/${encodeURIComponent(id)}`, researchStudySchema);
  },
  freeze(id: string): Promise<ResearchStudy> {
    return apiClient.request(`/api/research-studies/${encodeURIComponent(id)}/freeze`, researchStudySchema, { method: 'POST' });
  },
  runExploratory(id: string): Promise<ResearchStudy> {
    return apiClient.request(`/api/research-studies/${encodeURIComponent(id)}/exploratory-runs`, researchStudySchema, { method: 'POST' });
  },
  validateHoldout(id: string): Promise<ResearchStudy> {
    return apiClient.request(`/api/research-studies/${encodeURIComponent(id)}/validate-holdout`, researchStudySchema, { method: 'POST' });
  },
};
