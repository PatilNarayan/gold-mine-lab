import { useEffect, useRef } from 'react';
import {
  CandlestickSeries,
  ColorType,
  createChart,
  createSeriesMarkers,
  CrosshairMode,
  HistogramSeries,
  LineSeries,
} from 'lightweight-charts';
import type {
  IChartApi,
  ISeriesApi,
  CandlestickData,
  LineData,
  UTCTimestamp,
} from 'lightweight-charts';
import type { Candle, SeriesPoint, SignalAction } from '@/types/domain';
import { getChartPalette } from '@/charts/theme';
import { useUiStore } from '@/stores/useUiStore';

export interface ChartMarker {
  time: string;
  action: SignalAction;
  text?: string;
}

const toUtc = (iso: string) => Math.floor(Date.parse(iso) / 1000) as UTCTimestamp;

type Bar = Omit<Candle, 'volume'> & { volume: number | null };

interface ChartState {
  cs: ISeriesApi<'Candlestick'>;
  e20: ISeriesApi<'Line'>;
  e50: ISeriesApi<'Line'>;
  applied: { candles: Bar[]; ema20: SeriesPoint[]; ema50: SeriesPoint[] };
}

const toBar = (c: Bar): CandlestickData => ({
  time: toUtc(c.time),
  open: c.open,
  high: c.high,
  low: c.low,
  close: c.close,
});

const toPt = (p: SeriesPoint): LineData => ({ time: toUtc(p.time), value: p.value });

/**
 * Incremental tail update for the streaming path: when `next` is `prev` plus
 * an in-place rewrite of the forming bar (or one appended bucket), update()
 * moves just that bar. Any larger divergence (bootstrap, timeframe switch,
 * window roll) gets a full setData.
 */
function applyBars(series: ISeriesApi<'Candlestick'>, prev: Bar[] | undefined, next: Bar[]) {
  const sameWindow =
    prev &&
    prev.length > 0 &&
    next.length > 0 &&
    toUtc(next[0].time) === toUtc(prev[0].time) &&
    Math.abs(next.length - prev.length) <= 1;
  if (!sameWindow) {
    series.setData(next.map(toBar));
    return;
  }
  series.update(toBar(next[next.length - 1]));
}

function applyPoints(series: ISeriesApi<'Line'>, prev: SeriesPoint[] | undefined, next: SeriesPoint[]) {
  const sameWindow =
    prev &&
    prev.length > 0 &&
    next.length > 0 &&
    toUtc(next[0].time) === toUtc(prev[0].time) &&
    Math.abs(next.length - prev.length) <= 1;
  if (!sameWindow) {
    series.setData(next.map(toPt));
    return;
  }
  series.update(toPt(next[next.length - 1]));
}

/**
 * Candlestick chart with EMA overlays, volume and BUY/SELL markers.
 * Uses the lightweight-charts v5 API (`addSeries` + `createSeriesMarkers`).
 * The chart instance is created once per (height, theme); data changes update
 * series in place — the forming bar moves via series.update(), it never
 * rebuilds the chart per tick. Theme is re-read from CSS tokens whenever the
 * app theme changes.
 */
export function CandlestickChart({
  candles,
  ema20,
  ema50,
  markers = [],
  height = 440,
}: {
  candles: Bar[];
  ema20: SeriesPoint[];
  ema50: SeriesPoint[];
  markers?: ChartMarker[];
  height?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const theme = useUiStore((s) => s.theme);
  const stateRef = useRef<ChartState | null>(null);

  // Create the chart + series once; populate with the props at mount time.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const p = getChartPalette();

    let chart: IChartApi;
    try {
      chart = createChart(el, {
        width: el.clientWidth,
        height,
        layout: {
          background: { type: ColorType.Solid, color: 'transparent' },
          textColor: p.text3,
          fontFamily: p.fontMono,
          fontSize: 10,
          attributionLogo: false,
        },
        grid: {
          vertLines: { color: p.grid },
          horzLines: { color: p.grid },
        },
        crosshair: { mode: CrosshairMode.Normal },
        rightPriceScale: { borderColor: p.border },
        timeScale: { borderColor: p.border, timeVisible: true, secondsVisible: false },
      });
    } catch {
      return; // environments without canvas (e.g. jsdom)
    }

    const candlesSeries = chart.addSeries(CandlestickSeries, {
      upColor: p.buy,
      downColor: p.sell,
      wickUpColor: p.buy,
      wickDownColor: p.sell,
      borderVisible: false,
    });
    candlesSeries.setData(candles.map(toBar));

    const lineOpts = { lineWidth: 1 as const, crosshairMarkerVisible: false, priceLineVisible: false };
    const e20 = chart.addSeries(LineSeries, { ...lineOpts, color: p.info });
    e20.setData(ema20.map(toPt));
    const e50 = chart.addSeries(LineSeries, { ...lineOpts, color: p.warning });
    e50.setData(ema50.map(toPt));

    // Volume histogram only when the source actually supplies volume —
    // providers that don't report it (e.g. live XAU/USD) get no fake bars.
    const withVolume = candles.filter((c) => c.volume != null);
    if (withVolume.length === candles.length && withVolume.length > 0) {
      const volume = chart.addSeries(HistogramSeries, {
        priceScaleId: 'vol',
        priceFormat: { type: 'volume' },
      });
      chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.85, bottom: 0 } });
      volume.setData(
        candles.map((c) => ({
          time: toUtc(c.time),
          value: c.volume as number,
          color: c.close >= c.open ? p.buy : p.sell,
        })),
      );
    }

    // Markers must land on an existing bar — lightweight-charts clamps
    // unmatched times to the edge, stacking labels there. Snap to the
    // containing bar instead; drop markers before the first bar.
    const barTimes = candles.map((c) => toUtc(c.time)).sort((a, b) => a - b);
    const snap = (t: number): UTCTimestamp | null => {
      let lo = 0;
      let hi = barTimes.length - 1;
      let best = -1;
      while (lo <= hi) {
        const mid = (lo + hi) >> 1;
        if (barTimes[mid] <= t) {
          best = mid;
          lo = mid + 1;
        } else {
          hi = mid - 1;
        }
      }
      return best >= 0 ? barTimes[best] : null;
    };
    const seen = new Set<number>();
    const tradeMarkers = markers
      .filter((m) => m.action === 'BUY' || m.action === 'SELL')
      .map((m) => ({ m, t: snap(Date.parse(m.time) / 1000) }))
      .filter((x): x is { m: ChartMarker; t: UTCTimestamp } => x.t !== null && !seen.has(x.t) && (seen.add(x.t), true))
      .map(({ m, t }) => ({
        time: t,
        position: (m.action === 'BUY' ? 'belowBar' : 'aboveBar') as 'belowBar' | 'aboveBar',
        color: m.action === 'BUY' ? p.buy : p.sell,
        shape: (m.action === 'BUY' ? 'arrowUp' : 'arrowDown') as 'arrowUp' | 'arrowDown',
        text: m.text ?? m.action,
        size: 1,
      }));
    if (tradeMarkers.length) createSeriesMarkers(candlesSeries, tradeMarkers);

    chart.timeScale().fitContent();

    stateRef.current = { cs: candlesSeries, e20, e50, applied: { candles, ema20, ema50 } };

    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(() => chart.resize(el.clientWidth, el.clientHeight));
      ro.observe(el);
    }
    return () => {
      ro?.disconnect();
      stateRef.current = null;
      chart.remove();
    };
    // Chart is rebuilt only when layout-relevant props change; data flows
    // through the incremental effect below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [height, theme]);

  // Incremental data path: fold prop changes into the existing series —
  // forming-bar ticks use series.update(); bootstrap/timeframe changes
  // fall back to setData on the same chart instance.
  useEffect(() => {
    const s = stateRef.current;
    if (!s) return;
    if (s.applied.candles !== candles) applyBars(s.cs, s.applied.candles, candles);
    if (s.applied.ema20 !== ema20) applyPoints(s.e20, s.applied.ema20, ema20);
    if (s.applied.ema50 !== ema50) applyPoints(s.e50, s.applied.ema50, ema50);
    s.applied = { candles, ema20, ema50 };
  }, [candles, ema20, ema50]);

  return (
    <div className="relative">
      <div ref={ref} style={{ width: '100%', height }} role="img" aria-label="Candlestick chart" />
      <div className="pointer-events-none absolute left-2 top-2 flex gap-3 text-2xs text-content-3">
        <span className="flex items-center gap-1">
          <span className="inline-block h-0.5 w-4 bg-info" /> EMA20
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block h-0.5 w-4 bg-warning" /> EMA50
        </span>
      </div>
    </div>
  );
}
