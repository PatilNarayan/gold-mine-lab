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

export default function Market() {
  const assetId = useAssetStore((s) => s.assetId);
  const timeframe = useTimeframeStore((s) => s.timeframe);
  const setTimeframe = useTimeframeStore((s) => s.setTimeframe);

  const candles = useCandles(assetId, timeframe);
  const signals = useSignals({ assetId, limit: 200 });

  const markers: ChartMarker[] = (signals.data ?? [])
    .filter((s) => s.action !== 'NEUTRAL')
    .map((s) => ({ time: s.timestamp, action: s.action }));

  return (
    <div className="space-y-3">
      <DemoBanner />

      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="section-label">Market — {assetId.toUpperCase()}</h1>
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

      <section className="border border-line bg-surface-1 p-2" aria-label="Price chart">
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
            message="No candle data is available for this asset and timeframe."
          />
        )}
      </section>
    </div>
  );
}
