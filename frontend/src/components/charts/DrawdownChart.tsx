import { useMemo } from 'react';
import type { EquityPoint } from '@/types/domain';
import { echartsBaseOption, getChartPalette } from '@/charts/theme';
import { formatPercent } from '@/lib/formatters';
import { EChart } from './EChart';

/** Underwater drawdown chart (fractions <= 0 rendered as percent area). */
export function DrawdownChart({
  points,
  height = 200,
}: {
  points: EquityPoint[];
  height?: number;
}) {
  const option = useMemo(() => {
    const p = getChartPalette();
    const base = echartsBaseOption(p);
    return {
      ...base,
      tooltip: {
        ...base.tooltip,
        valueFormatter: (v: unknown) => (typeof v === 'number' ? formatPercent(v) : '—'),
      },
      yAxis: {
        ...base.yAxis,
        max: 0,
        axisLabel: {
          color: p.text3,
          fontSize: 10,
          formatter: (v: number) => formatPercent(v, 0),
        },
      },
      series: [
        {
          name: 'Drawdown',
          type: 'line',
          showSymbol: false,
          data: points.map((pt) => [pt.time, pt.drawdown]),
          lineStyle: { color: p.sell, width: 1 },
          itemStyle: { color: p.sell },
          areaStyle: { color: p.sell, opacity: 0.15 },
        },
      ],
    };
  }, [points]);

  return <EChart option={option} height={height} ariaLabel="Drawdown chart" />;
}
