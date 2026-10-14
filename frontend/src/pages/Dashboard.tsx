import { Activity, TrendingDown, TrendingUp } from 'lucide-react';
import {
  useAssets,
  useDashboardSummary,
  useMarketSummary,
  useSignals,
  useStrategies,
} from '@/hooks';
import { useAssetStore } from '@/stores/useAssetStore';
import { useTimeframeStore } from '@/stores/useTimeframeStore';
import {
  formatInteger,
  formatPercent,
  formatPrice,
  formatSignedCurrency,
  formatSignedPercent,
  formatTime,
} from '@/lib/formatters';
import { DemoBanner } from '@/components/common/DemoBanner';
import { StatCard } from '@/components/cards/StatCard';
import { Skeleton, SkeletonRows } from '@/components/common/Skeleton';
import { ErrorState } from '@/components/common/ErrorState';
import { SignalsTable } from '@/components/tables/SignalsTable';
import { EquityCurveChart } from '@/components/charts/EquityCurveChart';

export default function Dashboard() {
  const assetId = useAssetStore((s) => s.assetId);
  const timeframe = useTimeframeStore((s) => s.timeframe);

  const summary = useMarketSummary(assetId, timeframe);
  const strategies = useStrategies();
  const signals = useSignals({ limit: 10 });
  const perf = useDashboardSummary();
  const assets = useAssets();

  const sigCounts = (signals.data ?? []).reduce(
    (acc, s) => {
      acc[s.action] += 1;
      return acc;
    },
    { BUY: 0, SELL: 0, NEUTRAL: 0 },
  );

  return (
    <div className="space-y-3">
      <DemoBanner />

      {/* Market overview */}
      <section aria-label="Market overview" className="border border-line bg-surface-1 p-3">
        {summary.isLoading ? (
          <Skeleton className="h-16 w-full" />
        ) : summary.isError ? (
          <ErrorState error={summary.error} onRetry={() => summary.refetch()} />
        ) : summary.data ? (
          <div className="flex flex-wrap items-end justify-between gap-4">
            <div>
              <div className="section-label">Market overview</div>
              <div className="mt-1 flex items-baseline gap-2">
                <span className="text-xl font-semibold text-content-1">{summary.data.asset.symbol}</span>
                <span className="text-xs text-content-3">{summary.data.asset.name}</span>
              </div>
            </div>
            <div className="flex items-baseline gap-4">
              <span className="tnum text-2xl font-semibold">{formatPrice(summary.data.lastClose)}</span>
              <span
                className={`tnum flex items-center gap-1 text-sm font-medium ${
                  summary.data.change >= 0 ? 'text-buy' : 'text-sell'
                }`}
              >
                {summary.data.change >= 0 ? (
                  <TrendingUp className="h-4 w-4" aria-hidden />
                ) : (
                  <TrendingDown className="h-4 w-4" aria-hidden />
                )}
                {formatSignedCurrency(summary.data.change)} ({formatSignedPercent(summary.data.changePercent)})
              </span>
            </div>
            <div className="text-right text-2xs text-content-3">
              <div>Timeframe: <span className="tnum text-content-2">{timeframe}</span></div>
              <div>As of: <span className="tnum text-content-2">{formatTime(summary.data.asOf)} UTC</span></div>
              <div>
                Status: <span className="text-content-2">{summary.data.status}</span>
              </div>
            </div>
          </div>
        ) : null}
      </section>

      {/* Strategy overview */}
      <section aria-label="Strategy overview">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
          <StatCard label="Strategies" value={strategies.data ? formatInteger(strategies.data.length) : '—'} />
          <StatCard
            label="Active"
            value={strategies.data ? formatInteger(strategies.data.filter((s) => s.status === 'active').length) : '—'}
            tone="buy"
          />
          <StatCard label="BUY signals" value={formatInteger(sigCounts.BUY)} tone="buy" />
          <StatCard label="SELL signals" value={formatInteger(sigCounts.SELL)} tone="sell" />
          <StatCard label="Neutral" value={formatInteger(sigCounts.NEUTRAL)} />
          <StatCard
            label="Backtests"
            value={perf.data ? formatInteger(perf.data.totalBacktests) : '—'}
            icon={<Activity className="h-3.5 w-3.5" aria-hidden />}
          />
        </div>
      </section>

      <div className="grid gap-3 xl:grid-cols-5">
        {/* Recent signals */}
        <section aria-label="Recent signals" className="xl:col-span-3">
          <div className="mb-1.5 flex items-center justify-between">
            <h2 className="section-label">Latest signals</h2>
          </div>
          <SignalsTable
            signals={signals.data}
            strategies={strategies.data}
            assets={assets.data}
            isLoading={signals.isLoading}
            error={signals.error}
            onRetry={() => signals.refetch()}
          />
        </section>

        {/* Performance summary */}
        <section aria-label="Performance summary" className="xl:col-span-2">
          <h2 className="section-label mb-1.5">Performance summary</h2>
          {perf.isLoading ? (
            <SkeletonRows rows={4} height="h-10" />
          ) : perf.isError ? (
            <ErrorState error={perf.error} onRetry={() => perf.refetch()} />
          ) : perf.data ? (
            <div className="grid grid-cols-2 gap-2">
              <StatCard label="Total backtests" value={formatInteger(perf.data.totalBacktests)} />
              <StatCard
                label="Latest run — strategy"
                value={perf.data.latestRunStrategyName ?? '—'}
              />
              <StatCard label="Win rate (latest run)" value={formatPercent(perf.data.winRate)} />
              <StatCard
                label="Net P&L (latest run)"
                value={formatSignedCurrency(perf.data.netPnl)}
                tone={perf.data.netPnl >= 0 ? 'buy' : 'sell'}
              />
              <StatCard
                label="Max drawdown (latest)"
                value={formatPercent(perf.data.maxDrawdown)}
                tone="sell"
              />
              <StatCard label="Signals (recent)" value={formatInteger(signals.data?.length ?? 0)} />
            </div>
          ) : null}
        </section>
      </div>

      {/* Equity curve */}
      <section aria-label="Portfolio equity curve" className="border border-line bg-surface-1 p-3">
        <h2 className="section-label mb-2">
          Latest run equity curve
          {perf.data?.latestRunId ? (
            <span className="tnum text-content-3"> — {perf.data.latestRunId.slice(0, 8)}</span>
          ) : null}
        </h2>
        {perf.isLoading ? (
          <Skeleton className="h-[280px] w-full" />
        ) : perf.isError ? (
          <ErrorState error={perf.error} onRetry={() => perf.refetch()} />
        ) : perf.data && perf.data.equity.length ? (
          <EquityCurveChart points={perf.data.equity} height={280} title="Latest run equity" />
        ) : (
          <p className="text-xs text-content-3">No equity data.</p>
        )}
      </section>
    </div>
  );
}
