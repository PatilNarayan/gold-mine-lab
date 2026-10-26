import type { Asset, BacktestTrade, Strategy } from '@/types/domain';
import { formatDateTime, formatPrice, formatSignedCurrency } from '@/lib/formatters';
import { Badge } from '@/components/common/Badge';
import { DataTable, type Column } from './DataTable';

export function TradesTable({
  trades,
  strategies,
  assets,
  isLoading,
  error,
  onRetry,
  sortable = true,
}: {
  trades: BacktestTrade[] | undefined;
  strategies: Strategy[] | undefined;
  assets: Asset[] | undefined;
  isLoading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  sortable?: boolean;
}) {
  const stratName = new Map((strategies ?? []).map((s) => [s.id, s.name]));
  const assetSymbol = new Map((assets ?? []).map((a) => [a.id, a.symbol]));
  const sv = (fn: (t: BacktestTrade) => string | number) => (sortable ? fn : undefined);

  const columns: Column<BacktestTrade>[] = [
    {
      key: 'time',
      header: 'Timestamp',
      cell: (t) => <span className="tnum">{formatDateTime(t.entryTime)}</span>,
      sortValue: sv((t) => t.entryTime),
    },
    {
      key: 'symbol',
      header: 'Symbol',
      cell: (t) => assetSymbol.get(t.assetId) ?? t.assetId,
      sortValue: sv((t) => t.assetId),
    },
    {
      key: 'strategy',
      header: 'Strategy',
      cell: (t) => stratName.get(t.strategyId) ?? t.strategyId,
      sortValue: sv((t) => t.strategyId),
    },
    {
      key: 'side',
      header: 'Side',
      cell: (t) => <Badge tone={t.side === 'BUY' ? 'buy' : 'sell'}>{t.side}</Badge>,
      sortValue: sv((t) => t.side),
    },
    {
      key: 'entry',
      header: 'Entry',
      align: 'right',
      cell: (t) => formatPrice(t.entryPrice),
      sortValue: sv((t) => t.entryPrice),
    },
    {
      key: 'exit',
      header: 'Exit',
      align: 'right',
      cell: (t) => (t.exitPrice === null ? '—' : formatPrice(t.exitPrice)),
      sortValue: sv((t) => t.exitPrice ?? -Infinity),
    },
    {
      key: 'qty',
      header: 'Qty',
      align: 'right',
      cell: (t) => t.quantity.toFixed(2),
      sortValue: sv((t) => t.quantity),
    },
    {
      key: 'pnl',
      header: 'P&L',
      align: 'right',
      cell: (t) =>
        t.pnl === null ? (
          '—'
        ) : (
          <span className={t.pnl >= 0 ? 'text-buy' : 'text-sell'}>{formatSignedCurrency(t.pnl)}</span>
        ),
      sortValue: sv((t) => t.pnl ?? -Infinity),
    },
    {
      key: 'status',
      header: 'Status',
      cell: (t) =>
        t.status === 'open' ? (
          <Badge tone="info">Open</Badge>
        ) : (
          <Badge tone={t.result === 'win' ? 'buy' : t.result === 'loss' ? 'sell' : 'neutral'}>
            {t.result === 'breakeven' ? 'Even' : t.result === 'win' ? 'Win' : 'Loss'}
          </Badge>
        ),
      sortValue: sv((t) => t.result),
    },
  ];

  return (
    <DataTable
      columns={columns}
      rows={trades}
      rowKey={(t) => t.id}
      isLoading={isLoading}
      error={error}
      onRetry={onRetry}
      emptyTitle="No trades"
      emptyMessage="No trades match the current filters."
      ariaLabel="Trades"
    />
  );
}
