/**
 * Single chart theme shared by Lightweight Charts and ECharts. Colors are read
 * from the CSS design tokens at mount time so charts follow the active theme.
 */

export interface ChartPalette {
  surface: string;
  grid: string;
  border: string;
  text1: string;
  text2: string;
  text3: string;
  accent: string;
  buy: string;
  sell: string;
  neutral: string;
  warning: string;
  info: string;
  fontMono: string;
}

const FALLBACK: ChartPalette = {
  surface: '#0f131a',
  grid: '#1b2230',
  border: '#222a38',
  text1: '#e9edf3',
  text2: '#98a3b6',
  text3: '#5d6879',
  accent: '#c9a227',
  buy: '#34b877',
  sell: '#e5484d',
  neutral: '#8b96a8',
  warning: '#dd9c3c',
  info: '#5697f0',
  fontMono: '"JetBrains Mono", Consolas, monospace',
};

export function getChartPalette(): ChartPalette {
  if (typeof window === 'undefined') return FALLBACK;
  const cs = getComputedStyle(document.documentElement);
  const v = (name: string, fb: string) => cs.getPropertyValue(name).trim() || fb;
  return {
    surface: v('--surface-1', FALLBACK.surface),
    grid: v('--surface-3', FALLBACK.grid),
    border: v('--border', FALLBACK.border),
    text1: v('--text-1', FALLBACK.text1),
    text2: v('--text-2', FALLBACK.text2),
    text3: v('--text-3', FALLBACK.text3),
    accent: v('--accent', FALLBACK.accent),
    buy: v('--buy', FALLBACK.buy),
    sell: v('--sell', FALLBACK.sell),
    neutral: v('--neutral', FALLBACK.neutral),
    warning: v('--warning', FALLBACK.warning),
    info: v('--info', FALLBACK.info),
    fontMono: FALLBACK.fontMono,
  };
}

/** Shared ECharts base option fragments. */
export function echartsBaseOption(p: ChartPalette) {
  return {
    backgroundColor: 'transparent',
    textStyle: { color: p.text2, fontFamily: p.fontMono, fontSize: 11 },
    grid: { left: 8, right: 12, top: 28, bottom: 24, containLabel: true },
    tooltip: {
      trigger: 'axis',
      backgroundColor: p.surface,
      borderColor: p.border,
      textStyle: { color: p.text1, fontSize: 11, fontFamily: p.fontMono },
      axisPointer: { type: 'cross', label: { backgroundColor: p.border } },
    },
    legend: {
      top: 0,
      textStyle: { color: p.text3, fontSize: 10 },
      itemWidth: 14,
      itemHeight: 2,
    },
    xAxis: {
      type: 'time',
      axisLine: { lineStyle: { color: p.border } },
      axisLabel: { color: p.text3, fontSize: 10 },
      splitLine: { show: false },
    },
    yAxis: {
      type: 'value',
      scale: true,
      axisLabel: { color: p.text3, fontSize: 10 },
      splitLine: { lineStyle: { color: p.grid, width: 1 } },
    },
  } as const;
}
