import type { ComparisonService } from '../interfaces';
import type { Comparison, ComparisonRequest } from '@/types/domain';
import { simulateDelay, isEmptyScenario } from './scenario';
import { executeComparison, getComparison, listComparisons } from '@/mock/comparisons';
import { ApiError } from '@/types/api';

export const mockComparisonService: ComparisonService = {
  async run(request: ComparisonRequest): Promise<Comparison> {
    await simulateDelay();
    return executeComparison(request);
  },
  async list(): Promise<Comparison[]> {
    await simulateDelay();
    return isEmptyScenario() ? [] : listComparisons();
  },
  async get(id: string): Promise<Comparison> {
    await simulateDelay();
    const c = getComparison(id);
    if (!c) throw new ApiError('not_found', `Comparison ${id} not found`);
    return c;
  },
};
