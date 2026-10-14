import { z } from 'zod';
import type { Signal, Strategy, StrategyListItem } from '@/types/domain';
import { signalSchema, strategyListItemSchema, strategySchema } from '@/types/domain';
import type { StrategyService } from '@/services/interfaces';
import { apiClient } from './client';

export const httpStrategyService: StrategyService = {
  list(): Promise<StrategyListItem[]> {
    return apiClient.get('/api/strategies', z.array(strategyListItemSchema));
  },
  get(id: string): Promise<Strategy> {
    return apiClient.get(`/api/strategies/${encodeURIComponent(id)}`, strategySchema);
  },
  getSignals(opts?: { assetId?: string; strategyId?: string; limit?: number }): Promise<Signal[]> {
    const params = new URLSearchParams();
    if (opts?.assetId) params.set('asset', opts.assetId);
    if (opts?.strategyId) params.set('strategy', opts.strategyId);
    if (opts?.limit) params.set('limit', String(opts.limit));
    const qs = params.toString();
    return apiClient.get(`/api/signals${qs ? `?${qs}` : ''}`, z.array(signalSchema));
  },
};
