import { Link, useParams } from 'react-router-dom';
import { useAssets, useBacktestRun, useStrategies } from '@/hooks';
import {
  formatCurrency,
  formatInteger,
  formatPercent,
  formatSignedCurrency,
} from '@/lib/formatters';
import { DemoBanner } from '@/components/common/DemoBanner';
import { Badge } from '@/components/common/Badge';
import { Skeleton, SkeletonRows } from '@/components/common/Skeleton';
import { ErrorState } from '@/components/common/ErrorState';
import { MetricCard } from '@/components/cards/MetricCard';
import { EquityCurveChart } from '@/components/charts/EquityCurveChart';
import { DrawdownChart } from '@/components/charts/DrawdownChart';
import { TradesTable } from '@/components/tables/TradesTable';

export default function Results() {
  const { runId } = useParams<{ runId: string }>();
  const detail = useBacktestRun(runId);
  const strategies = useStrategies();
  const assets = useAssets();

  return (
    <div className="space-y-3">
      <DemoBanner />
      <div className="flex items-center justify-between">
        <h1 className="section-label">
          Backtest results {runId ? <span className="tnum text-content-2">— {runId}</span> : null}
        </h1>
        <Link to="/backtests" className="text-xs text-accent-fg hover:underline">
          New run →
        </Link>
      </div>

      {detail.isLoading ? (
        <div className="space-y-3">
          <SkeletonRows rows={2} height="h-16" />
          <Skeleton className="h-[280px] w-full" />
        </div>
      ) : detail.isError ? (
        <ErrorState
          error={detail.error}
          onRetry={() => detail.refetch()}
          title={`Could not load run "${runId}"`}
        />
      ) : detail.data ? (
        detail.data.run.status === 'failed' ? (
          <ErrorState
            error={new Error(detail.data.run.error ?? 'Run failed without details')}
            title={`Run ${detail.data.run.id} failed`}
          />
        ) : (
          <>
            <section aria-label="Run summary" className="border border-line bg-surface-1 p-3">
              <div className="flex flex-wrap items-center gap-2 text-xs text-content-2">
                <span className="font-medium text-content-1">
                  {detail.data.strategy?.name ?? detail.data.run.strategyId}
                </span>
                <Badge tone="accent">v{detail.data.strategy?.version ?? '?'}</Badge>
                <Badge tone={detail.data.run.status === 'completed' ? 'buy' : 'info'}>
                  {detail.data.run.status}
                </Badge>
                <span className="tnum text-content-3">
                  {detail.data.run.startDate} → {detail.data.run.endDate} · {detail.data.run.timeframe} ·
                  initial {formatCurrency(detail.data.run.initialCapital)}
                </span>
              </div>
            </section>

            {detail.data.metrics ? (
              <section
                aria-label="Metrics"
                className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-7"
              >
                <MetricCard
                  label="Net P&L"
                  value={formatSignedCurrency(detail.data.metrics.netPnl)}
                  tone={detail.data.metrics.netPnl >= 0 ? 'buy' : 'sell'}
                />
                <MetricCard
                  label="Return"
                  value={formatPercent(detail.data.metrics.returnPct)}
                  tone={detail.data.metrics.returnPct >= 0 ? 'buy' : 'sell'}
                />
                <MetricCard label="Win rate" value={formatPercent(detail.data.metrics.winRate)} />
                <MetricCard
                  label="Profit factor"
                  value={detail.data.metrics.profitFactor != null ? detail.data.metrics.profitFactor.toFixed(2) : '—'}
                  hint={detail.data.metrics.profitFactor == null ? 'No losing trades — undefined' : undefined}
                  tone={detail.data.metrics.profitFactor != null ? (detail.data.metrics.profitFactor >= 1 ? 'buy' : 'sell') : 'default'}
                />
                <MetricCard
                  label="Max drawdown"
                  value={formatPercent(detail.data.metrics.maxDrawdown)}
                  tone="sell"
                />
                <MetricCard label="Trades" value={formatInteger(detail.data.metrics.totalTrades)} />
                <MetricCard
                  label="Avg trade"
                  value={formatSignedCurrency(detail.data.metrics.avgTradePnl)}
                  tone={detail.data.metrics.avgTradePnl >= 0 ? 'buy' : 'sell'}
                />
              </section>
            ) : null}

            {detail.data.metrics ? (
              <section
                aria-label="Risk metrics"
                className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-7"
              >
                <MetricCard
                  label="Avg win"
                  hint="Average NET P&L of winning trades"
                  value={detail.data.metrics.avgWin != null ? formatSignedCurrency(detail.data.metrics.avgWin) : '—'}
                  tone="buy"
                />
                <MetricCard
                  label="Avg loss"
                  hint="Average NET loss, shown as a positive magnitude"
                  value={detail.data.metrics.avgLoss != null ? `-${formatCurrency(detail.data.metrics.avgLoss)}` : '—'}
                  tone="sell"
                />
                <MetricCard
                  label="Payoff"
                  hint="Average win magnitude vs average loss magnitude"
                  value={detail.data.metrics.payoffRatio != null ? detail.data.metrics.payoffRatio.toFixed(2) : '—'}
                />
                <MetricCard
                  label="Expectancy"
                  hint="Average NET P&L per closed trade"
                  value={detail.data.metrics.expectancy != null ? formatSignedCurrency(detail.data.metrics.expectancy) : '—'}
                  tone={detail.data.metrics.expectancy != null && detail.data.metrics.expectancy >= 0 ? 'buy' : 'sell'}
                />
                <MetricCard
                  label="Max cons. losses"
                  hint="Longest streak of net-losing trades"
                  value={detail.data.metrics.maxConsecutiveLosses != null ? formatInteger(detail.data.metrics.maxConsecutiveLosses) : '—'}
                />
                <MetricCard
                  label="Time in market"
                  hint="Share of evaluation-window bars with an open position (bar-based, not wall-clock hours)"
                  value={detail.data.metrics.timeInMarketPct != null ? `${detail.data.metrics.timeInMarketPct.toFixed(1)}%` : '—'}
                />
                <MetricCard
                  label="Recovery factor"
                  hint="Net P&L relative to the maximum drawdown amount"
                  value={detail.data.metrics.recoveryFactor != null ? detail.data.metrics.recoveryFactor.toFixed(2) : '—'}
                />
              </section>
            ) : null}

            <div className="grid gap-3 xl:grid-cols-2">
              <section className="border border-line bg-surface-1 p-3" aria-label="Equity curve">
                <h2 className="section-label mb-2">Equity curve</h2>
                <EquityCurveChart points={detail.data.equity} height={240} />
              </section>
              <section className="border border-line bg-surface-1 p-3" aria-label="Drawdown">
                <h2 className="section-label mb-2">Drawdown</h2>
                <DrawdownChart points={detail.data.equity} height={240} />
              </section>
            </div>

            <section aria-label="Trades">
              <h2 className="section-label mb-1.5">Trades ({detail.data.trades.length})</h2>
              <TradesTable
                trades={detail.data.trades}
                strategies={strategies.data}
                assets={assets.data}
              />
            </section>
          </>
        )
      ) : null}
    </div>
  );
}
