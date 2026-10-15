import type { ComparisonService } from '../interfaces';
import type { Comparison, ComparisonRequest } from '@/types/domain';
import { comparisonRequestSchema, comparisonSchema } from '@/types/domain';
import { apiClient } from './client';
import { z } from 'zod';

/** Real backend: comparisons execute sequentially server-side. */
export const httpComparisonService: ComparisonService = {
  run(request: ComparisonRequest): Promise<Comparison> {
    return apiClient.post('/api/comparisons', comparisonRequestSchema.parse(request), comparisonSchema);
  },
  list(): Promise<Comparison[]> {
    return apiClient.get('/api/comparisons', z.array(comparisonSchema));
  },
  get(id: string): Promise<Comparison> {
    return apiClient.get(`/api/comparisons/${id}`, comparisonSchema);
  },
};
