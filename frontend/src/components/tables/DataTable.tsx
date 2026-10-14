import { useMemo, useState, type ReactNode } from 'react';
import { ArrowDown, ArrowUp, ArrowUpDown } from 'lucide-react';
import { EmptyState } from '@/components/common/EmptyState';
import { ErrorState } from '@/components/common/ErrorState';
import { SkeletonRows } from '@/components/common/Skeleton';

export interface Column<T> {
  key: string;
  header: ReactNode;
  cell: (row: T) => ReactNode;
  align?: 'left' | 'right';
  className?: string;
  /** Provide to make the column sortable. */
  sortValue?: (row: T) => string | number;
}

type SortDir = 'asc' | 'desc';

/**
 * Generic dense data table with unified loading / empty / error states and
 * optional client-side column sorting. Scrolls horizontally inside its
 * container on narrow screens.
 */
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  isLoading = false,
  error = null,
  onRetry,
  emptyTitle = 'No rows',
  emptyMessage,
  onRowClick,
  dense = true,
  ariaLabel,
}: {
  columns: Column<T>[];
  rows: T[] | undefined;
  rowKey: (row: T) => string;
  isLoading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  emptyTitle?: string;
  emptyMessage?: ReactNode;
  onRowClick?: (row: T) => void;
  dense?: boolean;
  ariaLabel?: string;
}) {
  const [sort, setSort] = useState<{ key: string; dir: SortDir } | null>(null);

  const sorted = useMemo(() => {
    if (!rows || !sort) return rows;
    const col = columns.find((c) => c.key === sort.key);
    if (!col?.sortValue) return rows;
    const dir = sort.dir === 'asc' ? 1 : -1;
    return [...rows].sort((a, b) => {
      const va = col.sortValue!(a);
      const vb = col.sortValue!(b);
      if (va < vb) return -dir;
      if (va > vb) return dir;
      return 0;
    });
  }, [rows, sort, columns]);

  if (isLoading) {
    return (
      <div className="border border-line bg-surface-1 p-3">
        <SkeletonRows rows={6} />
      </div>
    );
  }
  if (error) {
    return <ErrorState error={error} onRetry={onRetry} />;
  }
  if (!sorted || sorted.length === 0) {
    return <EmptyState title={emptyTitle} message={emptyMessage} />;
  }

  const toggleSort = (key: string) => {
    setSort((s) =>
      s?.key === key ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' },
    );
  };

  return (
    <div className="overflow-x-auto border border-line bg-surface-1">
      <table className="w-full border-collapse text-left" aria-label={ariaLabel}>
        <thead>
          <tr className="border-b border-line">
            {columns.map((c) => {
              const active = sort?.key === c.key;
              const content = (
                <span className="inline-flex items-center gap-1">
                  {c.header}
                  {c.sortValue ? (
                    active ? (
                      sort.dir === 'asc' ? (
                        <ArrowUp className="h-3 w-3" aria-hidden />
                      ) : (
                        <ArrowDown className="h-3 w-3" aria-hidden />
                      )
                    ) : (
                      <ArrowUpDown className="h-3 w-3 opacity-40" aria-hidden />
                    )
                  ) : null}
                </span>
              );
              return (
                <th
                  key={c.key}
                  scope="col"
                  aria-sort={
                    active ? (sort.dir === 'asc' ? 'ascending' : 'descending') : undefined
                  }
                  className={`section-label whitespace-nowrap px-3 py-2 font-semibold ${
                    c.align === 'right' ? 'text-right' : 'text-left'
                  } ${c.className ?? ''}`}
                >
                  {c.sortValue ? (
                    <button
                      type="button"
                      onClick={() => toggleSort(c.key)}
                      className="inline-flex items-center gap-1 uppercase tracking-[0.14em] hover:text-content-1"
                    >
                      {content}
                    </button>
                  ) : (
                    content
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr
              key={rowKey(row)}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
              onKeyDown={
                onRowClick
                  ? (e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        onRowClick(row);
                      }
                    }
                  : undefined
              }
              tabIndex={onRowClick ? 0 : undefined}
              className={`border-b border-line/60 last:border-0 ${
                onRowClick ? 'cursor-pointer hover:bg-surface-2 focus-visible:bg-surface-2' : ''
              }`}
            >
              {columns.map((c) => (
                <td
                  key={c.key}
                  className={`whitespace-nowrap px-3 ${dense ? 'py-1.5' : 'py-2.5'} text-xs text-content-2 ${
                    c.align === 'right' ? 'tnum text-right' : ''
                  } ${c.className ?? ''}`}
                >
                  {c.cell(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
