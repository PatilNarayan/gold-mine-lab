import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useStrategies } from '@/hooks';
import type { StrategyListItem, StrategyStatus } from '@/types/domain';
import { formatPercent, formatSignedCurrency } from '@/lib/formatters';
import { DemoBanner } from '@/components/common/DemoBanner';
import { Badge } from '@/components/common/Badge';
import { SignalBadge } from '@/components/common/SignalBadge';
import { Input, Select } from '@/components/common/Input';
import { DataTable, type Column } from '@/components/tables/DataTable';

export default function Strategies() {
  const navigate = useNavigate();
  const strategies = useStrategies();
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState<StrategyStatus | 'all'>('all');

  const rows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (strategies.data ?? []).filter((s) => {
      if (status !== 'all' && s.status !== status) return false;
      if (q && !`${s.name} ${s.id} ${s.version}`.toLowerCase().includes(q)) return false;
      return true;
    });
  }, [strategies.data, search, status]);

  const columns: Column<StrategyListItem>[] = [
    {
      key: 'name',
      header: 'Strategy',
      sortValue: (s) => s.name,
      cell: (s) => (
        <div>
          <div className="font-medium text-content-1">{s.name}</div>
          <div className="text-2xs text-content-3">{s.id}</div>
        </div>
      ),
    },
    { key: 'version', header: 'Version', cell: (s) => <span className="tnum">v{s.version}</span>, sortValue: (s) => s.version },
    {
      key: 'asset',
      header: 'Asset',
      cell: (s) => s.supportedAssets.map((a) => a.toUpperCase()).join(', '),
      sortValue: (s) => s.supportedAssets.join(','),
    },
    { key: 'tf', header: 'Timeframe', cell: (s) => <span className="tnum">{s.timeframe}</span>, sortValue: (s) => s.timeframe },
    {
      key: 'status',
      header: 'Status',
      cell: (s) => <Badge tone={s.status === 'active' ? 'buy' : 'neutral'}>{s.status}</Badge>,
      sortValue: (s) => s.status,
    },
    {
      key: 'lastSignal',
      header: 'Last Signal',
      cell: (s) => (s.lastSignal ? <SignalBadge action={s.lastSignal.action} /> : <span className="text-content-3">—</span>),
      sortValue: (s) => s.lastSignal?.timestamp ?? '',
    },
    {
      key: 'performance',
      header: 'Latest run',
      align: 'right',
      cell: (s) =>
        s.performance.latestRunId ? (
          <span className={s.performance.latestRunNetPnl >= 0 ? 'text-buy' : 'text-sell'}>
            {formatSignedCurrency(s.performance.latestRunNetPnl)} ·{' '}
            {formatPercent(s.performance.latestRunWinRate, 0)} win
            <span className="block text-2xs text-content-3">
              {s.performance.latestRunTimeframe} · {s.performance.latestRunStartDate} →{' '}
              {s.performance.latestRunEndDate}
            </span>
          </span>
        ) : (
          <span className="text-content-3">no runs</span>
        ),
      sortValue: (s) => (s.performance.latestRunId ? s.performance.latestRunNetPnl : -Infinity),
    },
  ];

  return (
    <div className="space-y-3">
      <DemoBanner />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="section-label">Strategies</h1>
        <div className="flex items-center gap-2">
          <label htmlFor="strat-search" className="sr-only">
            Search strategies
          </label>
          <Input
            id="strat-search"
            type="search"
            placeholder="Search strategies…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="!w-48 !py-1 text-xs"
          />
          <label htmlFor="strat-status" className="sr-only">
            Status filter
          </label>
          <Select
            id="strat-status"
            value={status}
            onChange={(e) => setStatus(e.target.value as StrategyStatus | 'all')}
            className="!w-auto !py-1 text-xs"
          >
            <option value="all">All statuses</option>
            <option value="active">Active</option>
            <option value="inactive">Inactive</option>
          </Select>
        </div>
      </div>

      <DataTable
        columns={columns}
        rows={strategies.isLoading ? undefined : rows}
        rowKey={(s) => s.id}
        isLoading={strategies.isLoading}
        error={strategies.error}
        onRetry={() => strategies.refetch()}
        onRowClick={(s) => navigate(`/strategies/${s.id}`)}
        emptyTitle="No strategies"
        emptyMessage="No strategies match the current search or status filter."
        ariaLabel="Strategies"
      />
    </div>
  );
}
