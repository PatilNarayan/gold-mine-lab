import type { Signal, Strategy, StrategyListItem } from '@/types/domain';
import type { StrategyService } from '@/services/interfaces';
import { MOCK_STRATEGIES, strategyById } from '@/mock/strategies';
import { allSignals, latestSignalForStrategy, signalsForStrategy } from '@/mock/signals';
import { strategyPerformance } from '@/mock/backtests';
import { simulateDelay, isEmptyScenario } from './scenario';
import { ApiError } from '@/types/api';

export const mockStrategyService: StrategyService = {
  async list(): Promise<StrategyListItem[]> {
    await simulateDelay();
    if (isEmptyScenario()) return [];
    return MOCK_STRATEGIES.map((s) => ({
      ...s,
      lastSignal: latestSignalForStrategy(s.id),
      performance: strategyPerformance(s.id),
    }));
  },

  async get(id: string): Promise<Strategy> {
    await simulateDelay();
    const strategy = strategyById(id);
    if (!strategy) throw new ApiError('not_found', `Unknown strategy "${id}"`, 'mock');
    return strategy;
  },

  async getSignals(opts?: {
    assetId?: string;
    strategyId?: string;
    limit?: number;
  }): Promise<Signal[]> {
    await simulateDelay();
    if (isEmptyScenario()) return [];
    let signals = opts?.strategyId ? signalsForStrategy(opts.strategyId) : allSignals();
    if (opts?.assetId) signals = signals.filter((s) => s.assetId === opts.assetId);
    signals = [...signals].sort((a, b) => b.timestamp.localeCompare(a.timestamp));
    return opts?.limit ? signals.slice(0, opts.limit) : signals;
  },
};
