import type {
  BacktestDetail,
  BacktestRequest,
  BacktestRun,
  DashboardSummary,
} from '@/types/domain';
import type { BacktestService } from '@/services/interfaces';
import {
  dashboardSummary,
  executeBacktest,
  getRunDetail,
  latestCompletedRunId,
  listRuns,
  runsForStrategy,
} from '@/mock/backtests';
import { simulateDelay, isEmptyScenario, currentScenario } from './scenario';
import { ApiError } from '@/types/api';

export const mockBacktestService: BacktestService = {
  async listRuns(): Promise<BacktestRun[]> {
    await simulateDelay();
    return isEmptyScenario() ? [] : listRuns();
  },

  async runsForStrategy(strategyId: string): Promise<BacktestRun[]> {
    await simulateDelay();
    return isEmptyScenario() ? [] : runsForStrategy(strategyId);
  },

  async getRun(runId: string): Promise<BacktestDetail> {
    await simulateDelay();
    const detail = getRunDetail(runId);
    if (!detail) throw new ApiError('not_found', `Unknown backtest run "${runId}"`, 'mock');
    return detail;
  },

  async getLatestRunId(): Promise<string | null> {
    await simulateDelay();
    return isEmptyScenario() ? null : latestCompletedRunId();
  },

  async getSummary(): Promise<DashboardSummary> {
    await simulateDelay();
    if (isEmptyScenario()) {
      return {
        totalBacktests: 0,
        latestRunId: null,
        latestRunStrategyId: null,
        latestRunStrategyName: null,
        winRate: 0,
        netPnl: 0,
        maxDrawdown: 0,
        equity: [],
      };
    }
    return dashboardSummary();
  },

  async run(request: BacktestRequest): Promise<BacktestRun> {
    // Simulated execution time — the UI shows a progress state meanwhile.
    const ms = currentScenario() === 'slow' ? 3500 : 1600 + Math.random() * 600;
    await new Promise((r) => setTimeout(r, ms));
    if (currentScenario() === 'error') {
      throw new ApiError('mock_forced_error', 'Forced mock error scenario (?mock=error).', 'mock');
    }
    return executeBacktest(request);
  },
};
