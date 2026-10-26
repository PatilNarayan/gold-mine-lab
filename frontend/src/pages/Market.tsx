import { useState } from 'react';
import { useCandles, useSignals } from '@/hooks';
import { useAssetStore } from '@/stores/useAssetStore';
import { useTimeframeStore } from '@/stores/useTimeframeStore';
import { TIMEFRAMES } from '@/lib/constants';
import type { Timeframe } from '@/types/domain';
import { DemoBanner } from '@/components/common/DemoBanner';
import { Skeleton } from '@/components/common/Skeleton';
import { ErrorState } from '@/components/common/ErrorState';
import { EmptyState } from '@/components/common/EmptyState';
import { CandlestickChart, type ChartMarker } from '@/components/charts/CandlestickChart';
import { LiveMarketPanel } from '@/components/market/LiveMarketPanel';
import type { Coverage } from '@/types/domain';

type Source = 'historical' | 'live';

/** A one-line honest state of the synced historical coverage. */
function CoverageNote({ coverage }: { coverage: Coverage | undefined }) {
  if (!coverage) return null;
  const span =
    coverage.first && coverage.last
      ? `${coverage.first.slice(0, 10)} → ${coverage.last.slice(0, 10)}`
      : 'no verified span yet';
  const parts = [`${coverage.provider} · ${coverage.count} candles · ${span}`];
  if (coverage.inProgress) parts.push('syncing…');
  else if (coverage.state === 'rate_limited')
    parts.push(
      coverage.retryAt
        ? `provider rate-limited — retry ${new Date(coverage.retryAt).toLocaleTimeString()}`
        : 'provider rate-limited',
    );
  else if (coverage.state === 'failed') parts.push('sync failed — serving stored data');
  return (
    <div
      className={`mb-1 px-1 font-mono text-[10px] uppercase tracking-wide ${
        coverage.state === 'complete' ? 'text-content-3' : 'text-amber-400/90'
      }`}
    >
      {parts.join(' · ')}
    </div>
  );
}

function coverageEmptyMessage(coverage: Coverage | undefined): string {
  if (!coverage) return 'No candle data is available for this asset and timeframe.';
  if (coverage.inProgress) return 'History is syncing from the provider — check back shortly.';
  if (coverage.state === 'rate_limited')
    return 'History sync is provider rate-limited — stored coverage will resume shortly.';
  if (coverage.state === 'failed') return 'History sync failed — no stored candles yet.';
  return 'No candle data is available for this asset and timeframe.';
}

export default function Market() {
  const assetId = useAssetStore((s) => s.assetId);
  const timeframe = useTimeframeStore((s) => s.timeframe);
  const setTimeframe = useTimeframeStore((s) => s.setTimeframe);
  const [source, setSource] = useState<Source>('historical');

  const candles = useCandles(assetId, timeframe);
  const signals = useSignals({ assetId, limit: 200 });

  const markers: ChartMarker[] = (signals.data ?? [])
    .filter((s) => s.action !== 'NEUTRAL')
    .map((s) => ({ time: s.timestamp, action: s.action }));

  return (
    <div className="space-y-3">
      <DemoBanner />

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-3">
          <h1 className="section-label">Market — {assetId.toUpperCase()}</h1>
          <div className="flex gap-1" role="tablist" aria-label="Data source">
            {(['historical', 'live'] as const).map((s) => (
              <button
                key={s}
                type="button"
                role="tab"
                aria-selected={source === s}
                onClick={() => setSource(s)}
                className={`rounded-sm border px-2.5 py-1 text-xs font-medium capitalize ${
                  source === s
                    ? 'border-accent/50 bg-accent-soft text-accent-fg'
                    : 'border-line bg-surface-1 text-content-2 hover:bg-surface-2'
                }`}
              >
                {s}
              </button>
            ))}
          </div>
        </div>
        <div className="flex gap-1" role="tablist" aria-label="Timeframe">
          {TIMEFRAMES.map((t) => (
            <button
              key={t.value}
              type="button"
              role="tab"
              aria-selected={timeframe === t.value}
              onClick={() => setTimeframe(t.value as Timeframe)}
              className={`rounded-sm border px-2.5 py-1 text-xs font-medium ${
                timeframe === t.value
                  ? 'border-accent/50 bg-accent-soft text-accent-fg'
                  : 'border-line bg-surface-1 text-content-2 hover:bg-surface-2'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {source === 'live' ? (
        <LiveMarketPanel assetId={assetId} timeframe={timeframe} />
      ) : (
        <section className="border border-line bg-surface-1 p-2" aria-label="Price chart">
          <CoverageNote coverage={candles.data?.coverage} />
          {candles.isLoading ? (
            <Skeleton className="h-[440px] w-full" />
          ) : candles.isError ? (
            <ErrorState error={candles.error} onRetry={() => candles.refetch()} />
          ) : candles.data && candles.data.candles.length ? (
            <CandlestickChart
              candles={candles.data.candles}
              ema20={candles.data.overlays.ema20}
              ema50={candles.data.overlays.ema50}
              markers={markers}
              height={440}
            />
          ) : (
            <EmptyState
              title="No candles"
              message={coverageEmptyMessage(candles.data?.coverage)}
            />
          )}
        </section>
      )}
    </div>
  );
}
