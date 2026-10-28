import { z } from 'zod';
import type {
  BacktestDetail,
  BacktestRequest,
  BacktestRun,
  DashboardSummary,
  PortfolioSummary,
} from '@/types/domain';
import {
  backtestDetailSchema,
  backtestRequestSchema,
  backtestRunSchema,
  dashboardSummarySchema,
  portfolioSummarySchema,
} from '@/types/domain';
import type { BacktestService } from '@/services/interfaces';
import { apiClient } from './client';

export const httpBacktestService: BacktestService = {
  listRuns(): Promise<BacktestRun[]> {
    return apiClient.get('/api/backtests', z.array(backtestRunSchema));
  },
  runsForStrategy(strategyId: string): Promise<BacktestRun[]> {
    return apiClient.get(
      `/api/backtests?strategy=${encodeURIComponent(strategyId)}`,
      z.array(backtestRunSchema),
    );
  },
  getRun(runId: string): Promise<BacktestDetail> {
    return apiClient.get(`/api/backtests/${encodeURIComponent(runId)}`, backtestDetailSchema);
  },
  async getLatestRunId(): Promise<string | null> {
    const id = await apiClient.get('/api/backtests/latest', z.object({ runId: z.string().nullable() }));
    return id.runId;
  },
  getSummary(): Promise<DashboardSummary> {
    return apiClient.get('/api/backtests/summary', dashboardSummarySchema);
  },
  getPortfolioSummary(): Promise<PortfolioSummary> {
    return apiClient.get('/api/portfolio/summary', portfolioSummarySchema);
  },
  run(request: BacktestRequest): Promise<BacktestRun> {
    return apiClient.post('/api/backtests', backtestRequestSchema.parse(request), backtestRunSchema);
  },
};
