import { shadowDecisionsResponseSchema, shadowOrdersSchema, shadowStatusSchema } from '@/types/domain';
import type { ShadowService } from '@/services/interfaces';
import { apiClient } from './client';

/** Simulated-execution endpoints — hypothetical orders against real quotes. */
export const httpShadowService: ShadowService = {
  getStatus() {
    return apiClient.get('/api/shadow/status', shadowStatusSchema);
  },
  getOrders(limit = 100) {
    return apiClient.get(`/api/shadow/orders?limit=${limit}`, shadowOrdersSchema);
  },
  getDecisions(limit = 50) {
    return apiClient.get(`/api/shadow/decisions?limit=${limit}`, shadowDecisionsResponseSchema);
  },
};
