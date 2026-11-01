import { act, render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SeriesPoint } from '@/types/domain';
import { CandlestickChart } from './CandlestickChart';

// ---- fake lightweight-charts ------------------------------------------------
// jsdom has no canvas, so the real createChart can't run. The fake records
// every chart instance, its series, resize calls and range subscriptions —
// the RSI pane's whole lifecycle is observable through `mock.charts`.
const mock = vi.hoisted(() => {
  class FakeSeries {
    data: unknown[] = [];
    updateCalls = 0;
    setData(d: unknown[]) {
      this.data = d;
    }
    update() {
      this.updateCalls++;
    }
    createPriceLine() {}
  }
  class FakeTimeScale {
    handlers = new Set<(r: { from: number; to: number } | null) => void>();
    ranges: { from: number; to: number }[] = [];
    fitContent() {}
    subscribeVisibleLogicalRangeChange(h: (r: { from: number; to: number } | null) => void) {
      this.handlers.add(h);
    }
    unsubscribeVisibleLogicalRangeChange(h: (r: { from: number; to: number } | null) => void) {
      this.handlers.delete(h);
    }
    setVisibleLogicalRange(r: { from: number; to: number }) {
      this.ranges.push(r);
      this.emit(r); // real charts fire the subscription on programmatic sets
    }
    emit(r: { from: number; to: number } | null) {
      for (const h of [...this.handlers]) h(r);
    }
  }
  class FakeChart {
    series: FakeSeries[] = [];
    ts = new FakeTimeScale();
    resizes: Array<[number, number]> = [];
    removed = false;
    addSeries() {
      const s = new FakeSeries();
      this.series.push(s);
      return s;
    }
    timeScale() {
      return this.ts;
    }
    priceScale() {
      return { applyOptions() {} };
    }
    resize(w: number, h: number) {
      this.resizes.push([w, h]);
    }
    remove() {
      this.removed = true;
    }
  }
  const charts: FakeChart[] = [];
  return { charts, FakeChart };
});

vi.mock('lightweight-charts', () => ({
  CandlestickSeries: {},
  HistogramSeries: {},
  LineSeries: {},
  ColorType: { Solid: 'solid' },
  CrosshairMode: { Normal: 0 },
  LineStyle: { Dashed: 1 },
  createChart: () => {
    const c = new mock.FakeChart();
    mock.charts.push(c);
    return c;
  },
  createSeriesMarkers: () => {},
}));

const candles = Array.from({ length: 60 }, (_, i) => ({
  time: new Date(Date.UTC(2026, 0, 1) + i * 60_000).toISOString(),
  open: 100 + i,
  high: 102 + i,
  low: 99 + i,
  close: 101 + i,
  volume: null,
}));

const rsi: SeriesPoint[] = candles.map((c, i) => ({ time: c.time, value: 30 + i * 0.5 }));

const renderChart = (props: { rsi?: SeriesPoint[] } = {}) =>
  render(
    <CandlestickChart
      candles={candles}
      ema20={[]}
      ema50={[]}
      rsi={props.rsi ?? []}
      height={440}
    />,
  );

const main = () => mock.charts[0];
const rsiChart = () => mock.charts[mock.charts.length - 1];

describe('CandlestickChart RSI pane lifecycle', () => {
  beforeEach(() => {
    mock.charts.length = 0;
  });

  it('creates the RSI pane when data exists at mount', () => {
    renderChart({ rsi });
    expect(mock.charts).toHaveLength(2);
    expect(rsiChart().removed).toBe(false);
    expect(rsiChart().series[0].data).toHaveLength(rsi.length);
  });

  it('creates the RSI pane when data arrives after mount', () => {
    const { rerender } = renderChart({ rsi: [] });
    expect(mock.charts).toHaveLength(1);
    rerender(
      <CandlestickChart candles={candles} ema20={[]} ema50={[]} rsi={rsi} height={440} />,
    );
    expect(mock.charts).toHaveLength(2);
    expect(rsiChart().series[0].data).toHaveLength(rsi.length);
  });

  it('disposes the RSI pane and its subscriptions when data goes empty', () => {
    const { rerender } = renderChart({ rsi });
    const rsiC = rsiChart();
    rerender(
      <CandlestickChart candles={candles} ema20={[]} ema50={[]} rsi={[]} height={440} />,
    );
    expect(rsiC.removed).toBe(true);
    expect(main().ts.handlers.size).toBe(0); // toRsi subscription unlinked
    expect(rsiC.ts.handlers.size).toBe(0); // toMain subscription unlinked
  });

  it('creates a fresh pane on re-enable, with no duplicates or stale subscriptions', () => {
    const { rerender } = renderChart({ rsi });
    const chart = (r: SeriesPoint[]) => (
      <CandlestickChart candles={candles} ema20={[]} ema50={[]} rsi={r} height={440} />
    );
    rerender(chart([]));
    rerender(chart(rsi));
    const second = rsiChart();
    expect(second.removed).toBe(false);
    expect(mock.charts).toHaveLength(3); // main + 2 rsi panes over the session
    rerender(chart([]));
    rerender(chart(rsi));
    rerender(chart([]));
    rerender(chart(rsi));
    // Each enable creates exactly one new pane; each disable removed the old.
    expect(mock.charts).toHaveLength(5);
    expect(rsiChart()).not.toBe(second);
    expect(rsiChart().removed).toBe(false);
    expect(main().ts.handlers.size).toBe(1); // exactly one live subscription
  });

  it('removes both charts on unmount', () => {
    const { unmount } = renderChart({ rsi });
    unmount();
    expect(main().removed).toBe(true);
    expect(rsiChart().removed).toBe(true);
    expect(main().ts.handlers.size).toBe(0);
  });
});

describe('CandlestickChart RSI pane resizing', () => {
  beforeEach(() => {
    mock.charts.length = 0;
  });

  it('resizes both charts to the same width when the container resizes', () => {
    let roCb: ResizeObserverCallback | null = null;
    const observing = new Set<Element>();
    class RO implements ResizeObserver {
      constructor(cb: ResizeObserverCallback) {
        roCb = cb;
      }
      observe(el: Element) {
        observing.add(el);
      }
      unobserve() {}
      disconnect() {}
    }
    vi.stubGlobal('ResizeObserver', RO);

    const { container } = renderChart({ rsi });
    const el = container.querySelector('[aria-label="Candlestick chart"]') as HTMLElement;
    expect(observing.has(el)).toBe(true);
    Object.defineProperty(el, 'clientWidth', { value: 800, configurable: true });
    Object.defineProperty(el, 'clientHeight', { value: 440, configurable: true });

    act(() => roCb?.([], {} as ResizeObserver));

    expect(main().resizes).toContainEqual([800, 440]);
    expect(rsiChart().resizes).toContainEqual([800, 110]); // rsiHeight default

    vi.unstubAllGlobals();
  });
});

describe('CandlestickChart range synchronization', () => {
  beforeEach(() => {
    mock.charts.length = 0;
  });

  it('syncs the visible range both ways without an echo loop', () => {
    renderChart({ rsi });
    const m = main().ts;
    const r = rsiChart().ts;
    expect(m.handlers.size).toBe(1);
    expect(r.handlers.size).toBe(1);

    const r1 = { from: 10, to: 40 };
    m.emit(r1); // user scrolls the price chart
    expect(r.ranges).toEqual([r1]); // RSI chart followed
    expect(m.ranges).toEqual([]); // reentrancy guard stopped the echo back

    const r2 = { from: 20, to: 50 };
    r.emit(r2); // user scrolls the RSI chart
    expect(m.ranges).toEqual([r2]); // price chart followed
    expect(r.ranges).toEqual([r1]); // and again, no echo
  });
});
