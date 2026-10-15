import { z } from 'zod';
import { apiClient } from './client';
import {
  signalResearchSchema,
  type SignalResearch,
  type SignalResearchRequest,
} from '@/types/domain';
import type { SignalResearchService } from '../interfaces';

export const httpSignalResearchService: SignalResearchService = {
  run(request: SignalResearchRequest): Promise<SignalResearch> {
    return apiClient.post('/api/signal-research', request, signalResearchSchema);
  },
  list(): Promise<SignalResearch[]> {
    return apiClient.get('/api/signal-research', z.array(signalResearchSchema));
  },
  get(id: string): Promise<SignalResearch> {
    return apiClient.get(`/api/signal-research/${id}`, signalResearchSchema);
  },
};
