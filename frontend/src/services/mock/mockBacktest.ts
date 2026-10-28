import type {
  BacktestDetail,
  BacktestRequest,
  BacktestRun,
  DashboardSummary,
  PortfolioSummary,
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

  async getPortfolioSummary(): Promise<PortfolioSummary> {
    await simulateDelay();
    if (isEmptyScenario()) return { strategies: [] };
    // Deterministic experiment analytics — independent runs, not a portfolio.
    return {
      strategies: [
        {
          strategyId: 'ema-cross', runs: 3, totalNetPnl: 1240.5, totalTrades: 27,
          meanWinRate: 0.56, bestReturnPct: 8.4, worstReturnPct: -3.1, worstMaxDrawdown: 0.062,
        },
        {
          strategyId: 'mean-reversion', runs: 1, totalNetPnl: -412.75, totalTrades: 11,
          meanWinRate: 0.36, bestReturnPct: -2.9, worstReturnPct: -2.9, worstMaxDrawdown: 0.051,
        },
      ],
    };
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
