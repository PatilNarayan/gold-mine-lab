import { useLiveCandles, useLiveQuote, useLiveStatus, useLiveStream } from '@/hooks';
import type { Timeframe } from '@/types/domain';
import { Badge } from '@/components/common/Badge';
import { StatusDot } from '@/components/common/StatusDot';
import { Skeleton } from '@/components/common/Skeleton';
import { ErrorState } from '@/components/common/ErrorState';
import { EmptyState } from '@/components/common/EmptyState';
import { CandlestickChart } from '@/components/charts/CandlestickChart';
import { formatPrice, formatNumber } from '@/lib/formatters';

const stateDot = {
  disabled: 'muted',
  config_error: 'error',
  connecting: 'warn',
  connected: 'ok',
  stale: 'warn',
  disconnected: 'error',
} as const;

const stateLabel: Record<string, string> = {
  disabled: 'Live data disabled',
  config_error: 'Live data not configured',
  connecting: 'Connecting…',
  connected: 'Live',
  stale: 'Stale data',
  disconnected: 'Disconnected',
};

/**
 * Live XAUUSD market observation — provider-backed quotes and candles from
 * the backend's managed feed. Strictly observational: nothing here is a
 * backtest, a recommendation, or a promise of profitability.
 */
export function LiveMarketPanel({ assetId, timeframe }: { assetId: string; timeframe: Timeframe }) {
  const status = useLiveStatus();
  const st = status.data;
  const active = st?.enabled === true;

  const quote = useLiveQuote(assetId, active);
  const candles = useLiveCandles(assetId, timeframe, active);
  const stream = useLiveStream(assetId, active);

  if (status.isLoading) {
    return <Skeleton className="h-[440px] w-full" />;
  }

  if (!st || !st.enabled) {
    return (
      <EmptyState
        title="Live data is not configured"
        message="Set LIVE_MARKET_ENABLED=true plus a provider credential (TWELVE_DATA_API_KEY or TIINGO_API_TOKEN with LIVE_MARKET_PROVIDER) on the backend to stream real XAU/USD prices. Historical mode above keeps working without it."
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border border-line bg-surface-1 px-3 py-2">
        <StatusDot status={stateDot[st.state]} label={stateLabel[st.state] ?? st.state} />
        <Badge tone="info">{st.provider}</Badge>
        <Badge tone="neutral">
          {st.transport === 'websocket' ? 'WebSocket' : `REST ${st.pollIntervalSeconds}s poll`}
        </Badge>
        <Badge tone={stream === 'open' ? 'buy' : 'neutral'}>
          feed {stream === 'open' ? 'live' : stream}
        </Badge>
        <span className="tnum text-xs text-content-3">{st.symbol}</span>
        {quote.data ? (
          <span className="tnum text-sm font-semibold text-content-1">
            {formatPrice(quote.data.price, 2)} USD
            {quote.data.priceKind === 'mid' ? (
              <span
                className="ml-1 text-2xs font-normal text-content-3"
                title="Top-of-book midpoint (bid+ask)/2 — not an executed trade price"
              >
                mid
              </span>
            ) : null}
            {quote.data.status === 'stale' ? (
              <span className="ml-2 text-2xs font-normal text-warning">stale</span>
            ) : null}
          </span>
        ) : null}
        <span className="ml-auto flex items-center gap-4 text-xs text-content-2">
          {quote.data?.bid != null ? (
            <span className="tnum">
              Bid <span className="text-content-1">{formatPrice(quote.data.bid, 2)}</span>
            </span>
          ) : null}
          {quote.data?.ask != null ? (
            <span className="tnum">
              Ask <span className="text-content-1">{formatPrice(quote.data.ask, 2)}</span>
            </span>
          ) : null}
          {quote.data?.dayVolume != null ? (
            <span className="tnum">
              Vol <span className="text-content-1">{formatNumber(quote.data.dayVolume)}</span>
            </span>
          ) : null}
          {quote.data ? (
            <span
              className="tnum"
              title={
                quote.data.providerTimestamp
                  ? `provider ts ${quote.data.providerTimestamp} — fetched ${st.lastFetchAt ?? quote.data.receivedAt}`
                  : `no provider timestamp — fetched ${st.lastFetchAt ?? quote.data.receivedAt}`
              }
            >
              {quote.data.providerTimestamp ?? 'provider ts n/a'}
            </span>
          ) : null}
        </span>
      </div>

      {st.lastError ? (
        <div className="border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning" role="alert">
          {st.lastError}
        </div>
      ) : null}

      <section className="border border-line bg-surface-1 p-2" aria-label="Live price chart">
        {candles.isLoading ? (
          <Skeleton className="h-[440px] w-full" />
        ) : candles.isError ? (
          <ErrorState error={candles.error} onRetry={() => candles.refetch()} />
        ) : candles.data && candles.data.candles.length ? (
          <CandlestickChart
            candles={candles.data.candles}
            ema20={candles.data.overlays.ema20}
            ema50={candles.data.overlays.ema50}
            height={440}
          />
        ) : (
          <EmptyState
            title="No live candles yet"
            message="Waiting for provider data. This resolves once the stream delivers its first update, or when the candle preload completes."
          />
        )}
      </section>

      <p className="text-2xs text-content-3">
        {st.transport === 'rest'
          ? `REST polling every ${st.pollIntervalSeconds}s (est. ${st.estimatedDailyCredits.toLocaleString()} API credits/day) — periodic snapshots, not a tick-by-tick stream; the displayed time is the provider's quote time and may lag if the feed is delayed or the market is closed. `
          : ''}
        {quote.data?.priceKind === 'mid'
          ? 'Price shown is the top-of-book midpoint, not a last-traded price. '
          : ''}
        Live prices are market observation only — not a backtest, recommendation, or trading
        capability. Volume is hidden when the provider does not supply it.
      </p>
    </div>
  );
}
