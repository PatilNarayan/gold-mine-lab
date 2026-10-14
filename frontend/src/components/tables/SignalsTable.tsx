import type { Asset, Signal, Strategy } from '@/types/domain';
import { formatDateTime, formatPrice } from '@/lib/formatters';
import { SignalBadge } from '@/components/common/SignalBadge';
import { DataTable, type Column } from './DataTable';

export function SignalsTable({
  signals,
  strategies,
  assets,
  isLoading,
  error,
  onRetry,
}: {
  signals: Signal[] | undefined;
  strategies: Strategy[] | undefined;
  assets: Asset[] | undefined;
  isLoading?: boolean;
  error?: unknown;
  onRetry?: () => void;
}) {
  const stratName = new Map((strategies ?? []).map((s) => [s.id, s.name]));
  const assetSymbol = new Map((assets ?? []).map((a) => [a.id, a.symbol]));

  const columns: Column<Signal>[] = [
    {
      key: 'strategy',
      header: 'Strategy',
      cell: (s) => stratName.get(s.strategyId) ?? s.strategyId,
      sortValue: (s) => stratName.get(s.strategyId) ?? s.strategyId,
    },
    {
      key: 'signal',
      header: 'Signal',
      cell: (s) => <SignalBadge action={s.action} />,
      sortValue: (s) => s.action,
    },
    {
      key: 'score',
      header: 'Score',
      align: 'right',
      cell: (s) => s.score.toFixed(2),
      sortValue: (s) => s.score,
    },
    {
      key: 'price',
      header: 'Price',
      align: 'right',
      cell: (s) => formatPrice(s.price),
      sortValue: (s) => s.price,
    },
    {
      key: 'time',
      header: 'Time',
      cell: (s) => <span className="tnum">{formatDateTime(s.timestamp)}</span>,
      sortValue: (s) => s.timestamp,
    },
    {
      key: 'asset',
      header: 'Asset',
      cell: (s) => assetSymbol.get(s.assetId) ?? s.assetId,
      sortValue: (s) => s.assetId,
    },
  ];

  return (
    <DataTable
      columns={columns}
      rows={signals}
      rowKey={(s) => s.id}
      isLoading={isLoading}
      error={error}
      onRetry={onRetry}
      emptyTitle="No signals"
      emptyMessage="No strategy signals are available for this selection."
      ariaLabel="Signals"
    />
  );
}
