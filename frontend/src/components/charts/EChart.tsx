import { useEffect, useRef } from 'react';
import * as echarts from 'echarts/core';
import { LineChart } from 'echarts/charts';
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import type { EChartsCoreOption } from 'echarts/core';
import { useUiStore } from '@/stores/useUiStore';

echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, DataZoomComponent, CanvasRenderer]);

/**
 * Thin ECharts wrapper: init on mount, setOption on change, dispose on
 * unmount, resize via ResizeObserver. Remounts when the theme changes so the
 * palette is re-read from CSS tokens.
 */
export function EChart({
  option,
  height = 280,
  className = '',
  ariaLabel,
}: {
  option: EChartsCoreOption;
  height?: number;
  className?: string;
  ariaLabel?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);
  const theme = useUiStore((s) => s.theme);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let chart: echarts.ECharts | null = null;
    try {
      chart = echarts.init(el);
    } catch {
      // jsdom / no-canvas environments — render nothing rather than crash.
      return;
    }
    chartRef.current = chart;
    try {
      chart.setOption(option, true);
    } catch {
      // no-canvas environments (e.g. jsdom): init succeeded but paint failed
    }

    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(() => chart?.resize());
      ro.observe(el);
    }
    return () => {
      ro?.disconnect();
      chart?.dispose();
      chartRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [theme]);

  useEffect(() => {
    chartRef.current?.setOption(option, { notMerge: true });
  }, [option]);

  return (
    <div
      ref={ref}
      role="img"
      aria-label={ariaLabel}
      className={className}
      style={{ width: '100%', height }}
    />
  );
}
