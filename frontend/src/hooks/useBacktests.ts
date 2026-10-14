import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { services } from '@/services';
import type { BacktestRequest } from '@/types/domain';

export function useBacktestRuns() {
  return useQuery({
    queryKey: ['backtestRuns'],
    queryFn: () => services.backtest.listRuns(),
    staleTime: 15_000,
  });
}

export function useStrategyRuns(strategyId: string | undefined) {
  return useQuery({
    queryKey: ['backtestRuns', 'strategy', strategyId],
    queryFn: () => services.backtest.runsForStrategy(strategyId!),
    enabled: Boolean(strategyId),
    staleTime: 15_000,
  });
}

export function useBacktestRun(runId: string | undefined) {
  return useQuery({
    queryKey: ['backtestRun', runId],
    queryFn: () => services.backtest.getRun(runId!),
    enabled: Boolean(runId),
    staleTime: 60_000,
    retry: false,
  });
}

export function useLatestRunId() {
  return useQuery({
    queryKey: ['backtestRuns', 'latest'],
    queryFn: () => services.backtest.getLatestRunId(),
    staleTime: 15_000,
  });
}

export function useDashboardSummary() {
  return useQuery({
    queryKey: ['dashboardSummary'],
    queryFn: () => services.backtest.getSummary(),
    staleTime: 30_000,
  });
}

export function useRunBacktest() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: BacktestRequest) => services.backtest.run(req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['backtestRuns'] });
      qc.invalidateQueries({ queryKey: ['dashboardSummary'] });
    },
  });
}
