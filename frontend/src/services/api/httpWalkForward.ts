import type { WalkForwardService } from '../interfaces';
import type { WalkForward, WalkForwardRequest } from '@/types/domain';
import { walkForwardRequestSchema, walkForwardSchema } from '@/types/domain';
import { apiClient } from './client';
import { z } from 'zod';

/** Real backend: folds execute sequentially server-side. */
export const httpWalkForwardService: WalkForwardService = {
  preview(request: WalkForwardRequest): Promise<WalkForward> {
    return apiClient.post('/api/walkforwards/preview', walkForwardRequestSchema.parse(request), walkForwardSchema);
  },
  run(request: WalkForwardRequest): Promise<WalkForward> {
    return apiClient.post('/api/walkforwards', walkForwardRequestSchema.parse(request), walkForwardSchema);
  },
  list(): Promise<WalkForward[]> {
    return apiClient.get('/api/walkforwards', z.array(walkForwardSchema));
  },
  get(id: string): Promise<WalkForward> {
    return apiClient.get(`/api/walkforwards/${id}`, walkForwardSchema);
  },
};
