import { useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { useAssets, useStrategies, useTrades } from '@/hooks';
import { tradeFiltersFromSearchParams } from '@/lib/tradeFilters';
import { DemoBanner } from '@/components/common/DemoBanner';
import { Input, Select } from '@/components/common/Input';
import { Field } from '@/components/common/Field';
import { Button } from '@/components/common/Button';
import { TradesTable } from '@/components/tables/TradesTable';

/**
 * Trades explorer. Filter state lives entirely in URL query params so any
 * view is shareable; pagination is server-side-shaped (page/pageSize).
 */
export default function Trades() {
  const [searchParams, setSearchParams] = useSearchParams();
  const filters = useMemo(() => tradeFiltersFromSearchParams(searchParams), [searchParams]);

  const trades = useTrades(filters);
  const strategies = useStrategies();
  const assets = useAssets();

  const patchParams = (patch: Record<string, string | undefined>) => {
    const next = new URLSearchParams(searchParams);
    for (const [k, val] of Object.entries(patch)) {
      if (val === undefined || val === '') next.delete(k);
      else next.set(k, val);
    }
    // Any filter change resets pagination unless page is explicitly set.
    if (!('page' in patch)) next.delete('page');
    setSearchParams(next, { replace: true });
  };

  const totalPages = trades.data ? Math.max(1, Math.ceil(trades.data.total / trades.data.pageSize)) : 1;

  return (
    <div className="space-y-3">
      <DemoBanner />
      <h1 className="section-label">Trades</h1>

      {/* Filters — synced to URL query params */}
      <form
        className="grid grid-cols-2 gap-2 border border-line bg-surface-1 p-3 sm:grid-cols-3 lg:grid-cols-6"
        aria-label="Trade filters"
        onSubmit={(e) => e.preventDefault()}
      >
        <Field label="Strategy" htmlFor="tf-strategy">
          <Select
            id="tf-strategy"
            value={filters.strategyId ?? ''}
            onChange={(e) => patchParams({ strategy: e.target.value || undefined })}
          >
            <option value="">All strategies</option>
            {(strategies.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Side" htmlFor="tf-side">
          <Select
            id="tf-side"
            value={filters.side ?? ''}
            onChange={(e) => patchParams({ side: e.target.value || undefined })}
          >
            <option value="">Both</option>
            <option value="BUY">BUY</option>
            <option value="SELL">SELL</option>
          </Select>
        </Field>
        <Field label="Result" htmlFor="tf-result">
          <Select
            id="tf-result"
            value={filters.result ?? ''}
            onChange={(e) => patchParams({ result: e.target.value || undefined })}
          >
            <option value="">All</option>
            <option value="win">Win</option>
            <option value="loss">Loss</option>
            <option value="breakeven">Break-even</option>
            <option value="open">Open</option>
          </Select>
        </Field>
        <Field label="From" htmlFor="tf-from">
          <Input
            id="tf-from"
            type="date"
            className="tnum"
            value={filters.from ?? ''}
            onChange={(e) => patchParams({ from: e.target.value || undefined })}
          />
        </Field>
        <Field label="To" htmlFor="tf-to">
          <Input
            id="tf-to"
            type="date"
            className="tnum"
            value={filters.to ?? ''}
            onChange={(e) => patchParams({ to: e.target.value || undefined })}
          />
        </Field>
        <div className="flex items-end">
          <Button variant="secondary" size="sm" onClick={() => setSearchParams({}, { replace: true })}>
            Clear
          </Button>
        </div>
      </form>

      <TradesTable
        trades={trades.data?.items}
        strategies={strategies.data}
        assets={assets.data}
        isLoading={trades.isLoading}
        error={trades.error}
        onRetry={() => trades.refetch()}
      />

      {/* Pagination */}
      <div className="flex items-center justify-between text-xs text-content-3">
        <span>
          {trades.data ? (
            <>
              <span className="tnum">{trades.data.total}</span> trades · page{' '}
              <span className="tnum">{filters.page}</span> of <span className="tnum">{totalPages}</span>
              {trades.isFetching ? ' · refreshing…' : ''}
            </>
          ) : (
            '—'
          )}
        </span>
        <div className="flex gap-1">
          <Button
            variant="secondary"
            size="sm"
            disabled={filters.page <= 1}
            onClick={() => patchParams({ page: String(filters.page - 1) })}
            aria-label="Previous page"
          >
            <ChevronLeft className="h-3.5 w-3.5" aria-hidden />
            Prev
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={filters.page >= totalPages}
            onClick={() => patchParams({ page: String(filters.page + 1) })}
            aria-label="Next page"
          >
            Next
            <ChevronRight className="h-3.5 w-3.5" aria-hidden />
          </Button>
        </div>
      </div>
    </div>
  );
}
