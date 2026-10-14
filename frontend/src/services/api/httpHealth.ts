import type { HealthStatus } from '@/types/api';
import { healthSchema } from '@/types/api';
import type { HealthService } from '@/services/interfaces';
import { apiClient } from './client';

/** Real implementation — used to show "API connected / unreachable". */
export const httpHealthService: HealthService = {
  check(): Promise<HealthStatus> {
    return apiClient.get('/api/health', healthSchema, { timeoutMs: 5_000 });
  },
};
