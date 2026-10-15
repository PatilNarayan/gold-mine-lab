import { useMemo } from 'react';
import type { ComparisonResult } from '@/types/domain';
import { echartsBaseOption, getChartPalette } from '@/charts/theme';
import { formatCurrency, formatPercent } from '@/lib/formatters';
import { EChart } from './EChart';

const SERIES_COLORS = ['#c9a227', '#4f8ff7', '#3fa37c', '#c45c8f', '#8f6ef5', '#5fa8a0'];

/**
 * Multi-series overlay chart for comparison results — one line per member
 * run, each labeled `strategy · sample`. Independent legs are never merged;
 * every curve corresponds to exactly one persisted run.
 */
export function ComparisonChart({
  results,
  nameOf,
  field,
  height = 300,
}: {
  results: ComparisonResult[];
  nameOf: (strategyId: string) => string;
  field: 'equity' | 'drawdown';
  height?: number;
}) {
  const option = useMemo(() => {
    const p = getChartPalette();
    const series = results
      .filter((r) => r.equity.length > 0)
      .map((r, i) => ({
        name: `${nameOf(r.run.strategyId)} · ${r.sample === 'out_of_sample' ? 'OOS' : 'IS'}`,
        type: 'line' as const,
        showSymbol: false,
        data: r.equity.map((pt) => [pt.time, field === 'equity' ? pt.equity : pt.drawdown]),
        lineStyle: { color: SERIES_COLORS[i % SERIES_COLORS.length], width: 1.4 },
        itemStyle: { color: SERIES_COLORS[i % SERIES_COLORS.length] },
      }));
    return {
      ...echartsBaseOption(p),
      legend: { ...echartsBaseOption(p).legend, show: true, top: 0 },
      tooltip: {
        ...echartsBaseOption(p).tooltip,
        valueFormatter: (v: unknown) =>
          typeof v === 'number' ? (field === 'equity' ? formatCurrency(v) : formatPercent(v)) : '—',
      },
      series,
    };
  }, [results, nameOf, field]);

  return <EChart option={option} height={height} ariaLabel={`Comparison ${field} chart`} />;
}
