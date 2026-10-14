import { Link, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft } from 'lucide-react';
import { useAssets, useSignals, useStrategies, useStrategy, useStrategyRuns } from '@/hooks';
import type { BacktestRun, StrategyPerformance } from '@/types/domain';
import { strategyPerformance } from '@/mock/backtests';
import { formatDateTime, formatPercent, formatSignedCurrency } from '@/lib/formatters';
import { isMockMode } from '@/lib/config';
import { DemoBanner } from '@/components/common/DemoBanner';
import { Badge } from '@/components/common/Badge';
import { Skeleton, SkeletonRows } from '@/components/common/Skeleton';
import { ErrorState } from '@/components/common/ErrorState';
import { StatCard } from '@/components/cards/StatCard';
import { DataTable, type Column } from '@/components/tables/DataTable';
import { SignalsTable } from '@/components/tables/SignalsTable';
import { Button } from '@/components/common/Button';

export default function StrategyDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const strategy = useStrategy(id);
  const strategies = useStrategies();
  const assets = useAssets();
  const signals = useSignals({ strategyId: id, limit: 10 });
  const runs = useStrategyRuns(id);

  // Performance is precomputed per strategy; in mock mode read it from the
  // list row the strategy service already returns, falling back to the mock
  // aggregate for direct detail loads.
  const perf: StrategyPerformance | undefined =
    strategies.data?.find((s) => s.id === id)?.performance ??
    (isMockMode && strategy.data ? strategyPerformance(strategy.data.id) : undefined);

  const runColumns: Column<BacktestRun>[] = [
    { key: 'id', header: 'Run', cell: (r) => <span className="tnum">{r.id}</span>, sortValue: (r) => r.id },
    { key: 'range', header: 'Range', cell: (r) => <span className="tnum">{r.startDate} → {r.endDate}</span> },
    {
      key: 'status',
      header: 'Status',
      cell: (r) => (
        <Badge tone={r.status === 'completed' ? 'buy' : r.status === 'failed' ? 'sell' : 'info'}>
          {r.status}
        </Badge>
      ),
      sortValue: (r) => r.status,
    },
    { key: 'created', header: 'Created', cell: (r) => <span className="tnum">{formatDateTime(r.createdAt)}</span>, sortValue: (r) => r.createdAt },
  ];

  return (
    <div className="space-y-3">
      <DemoBanner />
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => navigate(-1)} aria-label="Back">
          <ArrowLeft className="h-4 w-4" aria-hidden />
        </Button>
        <h1 className="section-label">Strategy detail</h1>
      </div>

      {strategy.isLoading ? (
        <Skeleton className="h-40 w-full" />
      ) : strategy.isError ? (
        <ErrorState error={strategy.error} onRetry={() => strategy.refetch()} title="Strategy not found" />
      ) : strategy.data ? (
        <>
          <section className="border border-line bg-surface-1 p-3" aria-label="Strategy info">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2">
                  <h2 className="text-lg font-semibold text-content-1">{strategy.data.name}</h2>
                  <Badge tone="accent">v{strategy.data.version}</Badge>
                  <Badge tone={strategy.data.status === 'active' ? 'buy' : 'neutral'}>
                    {strategy.data.status}
                  </Badge>
                </div>
                <p className="mt-1 max-w-2xl text-xs text-content-2">{strategy.data.description}</p>
              </div>
              <div className="text-right text-2xs text-content-3">
                <div>
                  Timeframe: <span className="tnum text-content-2">{strategy.data.timeframe}</span>
                </div>
                <div className="mt-1 flex justify-end gap-1">
                  {strategy.data.supportedAssets.map((a) => (
                    <Badge key={a} tone="default">
                      {a.toUpperCase()}
                    </Badge>
                  ))}
                </div>
              </div>
            </div>
          </section>

          {perf ? (
            <section aria-label="Performance" className="grid grid-cols-2 gap-2 sm:grid-cols-3">
              <StatCard label="Backtests" value={perf.totalBacktests} />
              <StatCard
                label="Net P&L (all runs)"
                value={formatSignedCurrency(perf.netPnl)}
                tone={perf.netPnl >= 0 ? 'buy' : 'sell'}
              />
              <StatCard label="Avg win rate" value={formatPercent(perf.winRate)} />
            </section>
          ) : null}

          <div className="grid gap-3 lg:grid-cols-2">
            <section aria-label="Parameters" className="border border-line bg-surface-1 p-3">
              <h2 className="section-label mb-2">Parameters</h2>
              <table className="w-full text-left">
                <tbody>
                  {strategy.data.parameters.map((p) => (
                    <tr key={p.name} className="border-b border-line/60 last:border-0">
                      <td className="py-1.5 text-xs text-content-2">{p.label}</td>
                      <td className="tnum py-1.5 text-right text-xs text-content-1">
                        {String(p.default)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>

            <section aria-label="Latest signals">
              <h2 className="section-label mb-1.5">Latest signals</h2>
              <SignalsTable
                signals={signals.data}
                strategies={strategies.data}
                assets={assets.data}
                isLoading={signals.isLoading}
                error={signals.error}
                onRetry={() => signals.refetch()}
              />
            </section>
          </div>

          <section aria-label="Backtest history">
            <h2 className="section-label mb-1.5">Backtest history</h2>
            {runs.isLoading ? (
              <div className="border border-line bg-surface-1 p-3">
                <SkeletonRows rows={3} />
              </div>
            ) : (
              <DataTable
                columns={runColumns}
                rows={runs.data}
                rowKey={(r) => r.id}
                error={runs.error}
                onRetry={() => runs.refetch()}
                onRowClick={(r) => navigate(`/results/${r.id}`)}
                emptyTitle="No runs"
                emptyMessage={
                  <>
                    This strategy has no backtest runs yet.{' '}
                    <Link to="/backtests" className="text-accent-fg underline">
                      Run one
                    </Link>
                    .
                  </>
                }
                ariaLabel="Backtest history"
              />
            )}
          </section>
        </>
      ) : null}
    </div>
  );
}
