import { useNavigate } from 'react-router-dom';
import { useAssets, useBacktestRuns, useRunBacktest, useStrategies } from '@/hooks';
import type { BacktestRun } from '@/types/domain';
import { formatDateTime } from '@/lib/formatters';
import { ApiError } from '@/types/api';
import { DemoBanner } from '@/components/common/DemoBanner';
import { Badge } from '@/components/common/Badge';
import { SkeletonRows } from '@/components/common/Skeleton';
import { ErrorState } from '@/components/common/ErrorState';
import { DataTable, type Column } from '@/components/tables/DataTable';
import { PageHeader } from '@/components/common/PageHeader';
import { BacktestForm } from '@/components/forms/BacktestForm';

export default function Backtests() {
  const navigate = useNavigate();
  const strategies = useStrategies();
  const assets = useAssets();
  const runs = useBacktestRuns();
  const mutation = useRunBacktest();

  const runColumns: Column<BacktestRun>[] = [
    { key: 'id', header: 'Run', cell: (r) => <span className="tnum">{r.id}</span>, sortValue: (r) => r.id },
    {
      key: 'strategy',
      header: 'Strategy',
      cell: (r) => strategies.data?.find((s) => s.id === r.strategyId)?.name ?? r.strategyId,
      sortValue: (r) => r.strategyId,
    },
    {
      key: 'asset',
      header: 'Asset',
      cell: (r) => assets.data?.find((a) => a.id === r.assetId)?.symbol ?? r.assetId,
      sortValue: (r) => r.assetId,
    },
    { key: 'tf', header: 'TF', cell: (r) => <span className="tnum">{r.timeframe}</span> },
    {
      key: 'range',
      header: 'Range',
      cell: (r) => (
        <span className="tnum">
          {r.startDate} → {r.endDate}
        </span>
      ),
      sortValue: (r) => r.startDate,
    },
    {
      key: 'status',
      header: 'Status',
      cell: (r) => (
        <Badge
          tone={
            r.status === 'completed' ? 'buy' : r.status === 'failed' ? 'sell' : 'info'
          }
        >
          {r.status}
        </Badge>
      ),
      sortValue: (r) => r.status,
    },
    {
      key: 'data',
      header: 'Data',
      cell: (r) =>
        r.dataProvider ? (
          <Badge tone={r.dataProvider === 'csv' ? 'neutral' : 'info'}>
            {r.dataProvider}
            {r.dataCandles != null ? ` ${r.dataCandles}` : ''}
          </Badge>
        ) : (
          <span className="text-content-3">—</span>
        ),
      sortValue: (r) => r.dataProvider ?? '',
    },
    {
      key: 'created',
      header: 'Created',
      cell: (r) => <span className="tnum">{formatDateTime(r.createdAt)}</span>,
      sortValue: (r) => r.createdAt,
    },
  ];

  const loading = strategies.isLoading || assets.isLoading;

  return (
    <div className="space-y-3">
      <DemoBanner />
      <PageHeader
        title="Backtests"
        description="Replay a strategy over a historical window with explicit costs — in-sample evidence, not a forecast."
      />

      <section className="border border-line bg-surface-1 p-3" aria-label="New backtest">
        {loading ? (
          <SkeletonRows rows={4} />
        ) : strategies.isError || assets.isError ? (
          <ErrorState
            error={strategies.error ?? assets.error}
            onRetry={() => {
              strategies.refetch();
              assets.refetch();
            }}
          />
        ) : (
          <>
            <BacktestForm
              strategies={strategies.data ?? []}
              assets={assets.data ?? []}
              isPending={mutation.isPending}
              onRun={(req) =>
                mutation.mutate(req, {
                  onSuccess: (run) => navigate(`/results/${run.id}`),
                })
              }
            />
            {mutation.isError ? (
              <p className="mt-2 text-xs text-sell" role="alert">
                {mutation.error instanceof ApiError
                  ? mutation.error.message
                  : 'Backtest failed to start'}
              </p>
            ) : null}
          </>
        )}
      </section>

      <section aria-label="Run history">
        <h2 className="section-label mb-1.5">Run history</h2>
        <DataTable
          columns={runColumns}
          rows={runs.data}
          rowKey={(r) => r.id}
          isLoading={runs.isLoading}
          error={runs.error}
          onRetry={() => runs.refetch()}
          rowHref={(r) => `/results/${r.id}`}
          rowLinkLabel={(r) => `Open results for run ${r.id}`}
          emptyTitle="No runs yet"
          emptyMessage="Run a backtest above and it will appear here."
          ariaLabel="Backtest run history"
        />
      </section>
    </div>
  );
}
