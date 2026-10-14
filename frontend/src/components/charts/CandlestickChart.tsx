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
import type { IChartApi, UTCTimestamp } from 'lightweight-charts';
import type { Candle, SeriesPoint, SignalAction } from '@/types/domain';
import { getChartPalette } from '@/charts/theme';
import { useUiStore } from '@/stores/useUiStore';

export interface ChartMarker {
  time: string;
  action: SignalAction;
  text?: string;
}

const toUtc = (iso: string) => Math.floor(Date.parse(iso) / 1000) as UTCTimestamp;

/**
 * Candlestick chart with EMA overlays, volume and BUY/SELL markers.
 * Uses the lightweight-charts v5 API (`addSeries` + `createSeriesMarkers`).
 * Theme is re-read from CSS tokens whenever the app theme changes.
 */
export function CandlestickChart({
  candles,
  ema20,
  ema50,
  markers = [],
  height = 440,
}: {
  candles: Candle[];
  ema20: SeriesPoint[];
  ema50: SeriesPoint[];
  markers?: ChartMarker[];
  height?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const theme = useUiStore((s) => s.theme);

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
    candlesSeries.setData(
      candles.map((c) => ({
        time: toUtc(c.time),
        open: c.open,
        high: c.high,
        low: c.low,
        close: c.close,
      })),
    );

    const lineOpts = { lineWidth: 1 as const, crosshairMarkerVisible: false, priceLineVisible: false };
    const e20 = chart.addSeries(LineSeries, { ...lineOpts, color: p.info });
    e20.setData(ema20.map((pt) => ({ time: toUtc(pt.time), value: pt.value })));
    const e50 = chart.addSeries(LineSeries, { ...lineOpts, color: p.warning });
    e50.setData(ema50.map((pt) => ({ time: toUtc(pt.time), value: pt.value })));

    const volume = chart.addSeries(HistogramSeries, {
      priceScaleId: 'vol',
      priceFormat: { type: 'volume' },
    });
    chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.85, bottom: 0 } });
    volume.setData(
      candles.map((c) => ({
        time: toUtc(c.time),
        value: c.volume,
        color: c.close >= c.open ? p.buy : p.sell,
      })),
    );

    const tradeMarkers = markers
      .filter((m) => m.action === 'BUY' || m.action === 'SELL')
      .map((m) => ({
        time: toUtc(m.time),
        position: (m.action === 'BUY' ? 'belowBar' : 'aboveBar') as 'belowBar' | 'aboveBar',
        color: m.action === 'BUY' ? p.buy : p.sell,
        shape: (m.action === 'BUY' ? 'arrowUp' : 'arrowDown') as 'arrowUp' | 'arrowDown',
        text: m.text ?? m.action,
        size: 1,
      }));
    if (tradeMarkers.length) createSeriesMarkers(candlesSeries, tradeMarkers);

    chart.timeScale().fitContent();

    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(() => chart.resize(el.clientWidth, el.clientHeight));
      ro.observe(el);
    }
    return () => {
      ro?.disconnect();
      chart.remove();
    };
  }, [candles, ema20, ema50, markers, height, theme]);

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
