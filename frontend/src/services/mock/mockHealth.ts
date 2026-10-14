import type { HealthStatus } from '@/types/api';
import type { HealthService } from '@/services/interfaces';
import { simulateDelay } from './scenario';

export const mockHealthService: HealthService = {
  async check(): Promise<HealthStatus> {
    await simulateDelay();
    return { status: 'ok', service: 'goldmine-lab (mock)', database: 'n/a — mock data' };
  },
};
