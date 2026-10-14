import { useMemo } from 'react';
import type { EquityPoint } from '@/types/domain';
import { echartsBaseOption, getChartPalette } from '@/charts/theme';
import { formatCurrency } from '@/lib/formatters';
import { EChart } from './EChart';

/** Equity curve (line + soft area fill) with crosshair, tooltip and legend. */
export function EquityCurveChart({
  points,
  height = 280,
  title = 'Equity',
}: {
  points: EquityPoint[];
  height?: number;
  title?: string;
}) {
  const option = useMemo(() => {
    const p = getChartPalette();
    return {
      ...echartsBaseOption(p),
      tooltip: {
        ...echartsBaseOption(p).tooltip,
        valueFormatter: (v: unknown) => (typeof v === 'number' ? formatCurrency(v) : '—'),
      },
      series: [
        {
          name: title,
          type: 'line',
          showSymbol: false,
          data: points.map((pt) => [pt.time, pt.equity]),
          lineStyle: { color: p.accent, width: 1.5 },
          itemStyle: { color: p.accent },
          areaStyle: { color: p.accent, opacity: 0.08 },
        },
      ],
    };
  }, [points, title]);

  return <EChart option={option} height={height} ariaLabel="Equity curve chart" />;
}
