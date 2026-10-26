import { Badge } from '@/components/common/Badge';
import { EmptyState } from '@/components/common/EmptyState';
import { ErrorState } from '@/components/common/ErrorState';
import { Skeleton } from '@/components/common/Skeleton';
import { useLiveStatus, useShadowDecisions, useShadowOrders, useShadowStatus } from '@/hooks';
import type { ShadowOrder } from '@/types/domain';
import { formatCurrency, formatDateTime, formatNumber } from '@/lib/formatters';

/**
 * Shadow Trading — hypothetical execution against real quote events.
 * Everything on this page is SIMULATED: signal decisions on confirmed-
 * closed candles, fills at observed ask (entry) / bid (exit). EMA20/50 is
 * a control for plumbing validation — it has no demonstrated edge.
 */
export default function Shadow() {
  const status = useShadowStatus();
  const orders = useShadowOrders(100);
  const decisions = useShadowDecisions(20);
  const live = useLiveStatus();

  if (status.isLoading) return <Skeleton className="h-64" />;
  if (status.error || !status.data) {
    return <ErrorState error={status.error ?? new Error('empty')} title="Shadow engine status unavailable" />;
  }
  const s = status.data;
  const m = s.metrics;
  const rows = orders.data?.orders ?? [];
  const open = s.position;
  const pending = s.pendingOrder;

  // Operational state: honest composite, never "valid because connected".
  const liveState = live.data?.state ?? 'unknown';
  const quoteAgeSec = m?.lastQuoteAt
    ? Math.max(0, Math.floor((Date.now() - new Date(m.lastQuoteAt).getTime()) / 1000))
    : null;
  const quoteFresh = quoteAgeSec != null && quoteAgeSec <= 30;
  const opState = liveState === 'stale'
    ? { label: 'connected — quotes stale', tone: 'warning' as const }
    : liveState !== 'connected'
      ? { label: `provider ${liveState}`, tone: 'neutral' as const }
    : !quoteFresh
      ? { label: quoteAgeSec == null ? 'no quotes yet' : `quotes stale (${quoteAgeSec}s)`, tone: 'warning' as const }
      : pending
        ? { label: 'order awaiting qualifying quote', tone: 'warning' as const }
        : open
          ? { label: 'position open', tone: 'buy' as const }
          : { label: 'watching — no eligible signal yet', tone: 'info' as const };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold text-content-1">Shadow Trading</h1>
        <Badge tone="warning">SIMULATED — no real orders</Badge>
        <Badge tone={opState.tone}>{opState.label}</Badge>
        <span className="text-2xs text-content-3">
          {s.strategyId} v{s.strategyVersion} · {s.symbol} · {s.timeframe} · fills at observed bid/ask
        </span>
      </div>

      {s.session && (
        <div className="flex flex-wrap gap-x-6 gap-y-1 rounded-sm border border-line bg-surface-1 px-3 py-2 text-2xs text-content-3">
          <span>session #{s.session.id} · {s.session.provider}</span>
          {s.recorder && (
            <span>
              recorder #{s.recorder.coverageId}
              {s.recorder.feedState && ` · feed ${s.recorder.feedState}`}
              {s.recorder.lastObservationAt
                ? ` · last obs ${formatDateTime(s.recorder.lastObservationAt)}`
                : ' · no obs yet'}
              {s.recorder.droppedBatches > 0 && ` · ${s.recorder.droppedBatches} dropped batches`}
              {s.recorder.lastFeedDegradedAt && ` · feed degraded ${formatDateTime(s.recorder.lastFeedDegradedAt)}`}
            </span>
          )}
          {!s.recorder && <span>recorder coverage unknown</span>}
          {s.session.startedAt && <span>started {formatDateTime(s.session.startedAt)}</span>}
          <span>restarts {s.session.restartCount}</span>
          {m && (
            <span>
              bars {m.barsProcessed} · signals {m.signalsSeen} · quotes {m.quotesSeen}
              {m.quotesRejected > 0 && ` (${m.quotesRejected} rejected)`} ·
              recoveries {m.recoveries} · dup-skips {m.duplicateEventsSkipped}
            </span>
          )}
          {m?.lastQuoteAt && <span>last quote {formatDateTime(m.lastQuoteAt)}</span>}
          {m?.lastFillAt && <span>last fill {formatDateTime(m.lastFillAt)}</span>}
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Virtual equity" value={formatCurrency(s.equity)} />
        <Stat label="Realized P&L" value={formatCurrency(s.realizedPnl)} />
        <Stat label="Commissions (assumed)" value={formatCurrency(s.realizedCommissions)} />
        <Stat label="Spread cost (measured)" value={formatCurrency(s.realizedSpreadCost)} />
      </div>

      {open ? (
        <section className="rounded-sm border border-line bg-surface-1 p-3">
          <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-content-3">
            Open hypothetical position
          </h2>
          <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm text-content-1">
            <span><Badge tone="buy">{open.side}</Badge></span>
            <span>qty {open.quantity.toFixed(4)}</span>
            <span>entry ask {formatCurrency(open.entryPrice)}</span>
            {open.entryAt && <span>filled {formatDateTime(open.entryAt)}</span>}
          </div>
        </section>
      ) : (
        <p className="text-sm text-content-3">No open hypothetical position.</p>
      )}

      {pending && (
        <section className="rounded-sm border border-warning/40 bg-warning/10 p-3">
          <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-content-3">
            Pending decision — awaiting qualifying quote
          </h2>
          <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm text-content-1">
            <Badge tone={pending.side === 'BUY' ? 'buy' : 'sell'}>{pending.side}</Badge>
            <span>signal bar {formatDateTime(pending.signalBarTime)}</span>
            <span>decided {formatDateTime(pending.decisionAt)}</span>
          </div>
        </section>
      )}

      {/* Decision ledger — every evaluated close, incl. no-signal */}
      <section className="rounded-sm border border-line bg-surface-1 p-3">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-content-3">
            Recent evaluations
          </h2>
          <span className="text-2xs text-content-3">every confirmed close — evidence the strategy ran</span>
        </div>
        {(decisions.data?.decisions?.length ?? 0) === 0 ? (
          <EmptyState
            title="No evaluations recorded yet"
            message="Each confirmed candle close records its strategy verdict here."
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead className="border-b border-line text-2xs uppercase tracking-wide text-content-3">
                <tr>
                  <th className="pb-1 font-medium">bar</th>
                  <th className="pb-1 font-medium">src</th>
                  <th className="pb-1 font-medium">action</th>
                  <th className="pb-1 font-medium">fast/slow EMA</th>
                  <th className="pb-1 font-medium">outcome</th>
                </tr>
              </thead>
              <tbody>
                {decisions.data!.decisions.slice(0, 8).map((d) => (
                  <tr key={d.id} className="border-b border-line/40 last:border-0">
                    <td className="py-1.5">{formatDateTime(d.barTime)}</td>
                    <td className="py-1.5 text-content-3">{d.source}</td>
                    <td className="py-1.5">
                      <Badge tone={d.action === 'buy' ? 'buy' : d.action === 'sell' ? 'sell' : 'neutral'}>
                        {d.action}
                      </Badge>
                    </td>
                    <td className="py-1.5 tabular-nums">
                      {d.fastEma != null && d.slowEma != null
                        ? `${formatNumber(d.fastEma)} / ${formatNumber(d.slowEma)}`
                        : '—'}
                    </td>
                    <td className="py-1.5 text-content-3">
                      {d.actionable ? `→ order #${d.orderId}` : (d.blockedReason ?? d.reason)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="rounded-sm border border-line bg-surface-1 p-3">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-content-3">
            Hypothetical orders
          </h2>
          <span className="text-2xs text-content-3">
            filled {s.orderStats.filled} · expired {s.orderStats.expired} ·
            missed {s.orderStats.missed} · unobservable {s.orderStats.unobservable} ·
            rejected {s.orderStats.rejected} · pending {s.orderStats.pending}
          </span>
        </div>
        {rows.length === 0 ? (
          <EmptyState title="No shadow orders yet" message="Decisions appear after confirmed candle closes." />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead className="border-b border-line text-2xs uppercase tracking-wide text-content-3">
                <tr>
                  <th className="px-2 py-1.5">Side</th>
                  <th className="px-2 py-1.5">Status</th>
                  <th className="px-2 py-1.5">Signal bar</th>
                  <th className="px-2 py-1.5">Fill</th>
                  <th className="px-2 py-1.5">Bid / Ask</th>
                  <th className="px-2 py-1.5">Qty</th>
                  <th className="px-2 py-1.5">Reason</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((o) => <OrderRow key={o.id} o={o} />)}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <p className="text-2xs text-content-3">
        Execution model: signals evaluate only on confirmed-closed candles; fills execute at the
        first valid fresh quote after the decision — entry at ask, exit at bid. Mid is recorded,
        never executed. Quotes missing legs, crossed, stale, or predating the decision are rejected.
        The EMA20/50 benchmark is a plumbing control — it has not demonstrated a repeatable edge.
      </p>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-sm border border-line bg-surface-1 px-3 py-2">
      <div className="text-2xs uppercase tracking-wide text-content-3">{label}</div>
      <div className="mt-0.5 text-sm font-semibold text-content-1">{value}</div>
    </div>
  );
}

function OrderRow({ o }: { o: ShadowOrder }) {
  const tone = o.status === 'filled' ? 'buy'
    : o.status === 'pending' ? 'warning'
    : o.status === 'missed' ? 'sell'
    : o.status === 'unobservable' ? 'neutral'
    : 'neutral';
  return (
    <tr className="border-b border-line/50 text-content-2">
      <td className="px-2 py-1.5">
        <Badge tone={o.side === 'BUY' ? 'buy' : 'sell'}>{o.side}</Badge>
      </td>
      <td className="px-2 py-1.5"><Badge tone={tone}>{o.status}</Badge></td>
      <td className="px-2 py-1.5">{formatDateTime(o.signalBarTime)}</td>
      <td className="px-2 py-1.5">{o.fillPrice != null ? formatCurrency(o.fillPrice) : '—'}</td>
      <td className="px-2 py-1.5">
        {o.quoteBid != null && o.quoteAsk != null
          ? `${o.quoteBid.toFixed(2)} / ${o.quoteAsk.toFixed(2)}`
          : '—'}
      </td>
      <td className="px-2 py-1.5">{o.quantity != null ? o.quantity.toFixed(4) : '—'}</td>
      <td className="px-2 py-1.5 text-content-3">{o.rejectReason ?? '—'}</td>
    </tr>
  );
}
