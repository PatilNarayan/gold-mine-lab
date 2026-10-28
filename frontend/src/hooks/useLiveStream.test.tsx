import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('@/lib/config', () => ({
  getApiUrl: () => 'http://unit.test:8080',
  isMockMode: false,
}));

import { useLiveStream } from './useLiveStream';

// Minimal controllable WebSocket: the hook assigns on* handlers and calls
// close(); tests drive open/message/close events explicitly.
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  readonly url: string;
  readyState = 0;
  closed = false;
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onclose: ((ev: Event) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }
  close() {
    this.closed = true;
    this.readyState = 3;
  }
  simulateOpen() {
    this.readyState = 1;
    this.onopen?.(new Event('open'));
  }
  simulateMessage(data: unknown) {
    this.onmessage?.({ data: typeof data === 'string' ? data : JSON.stringify(data) } as MessageEvent);
  }
  simulateClose() {
    this.readyState = 3;
    this.onclose?.(new Event('close'));
  }
}

let qc: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

beforeEach(() => {
  vi.useFakeTimers();
  FakeWebSocket.instances = [];
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const quoteFrame = (price: number) => ({
  type: 'quote',
  quote: {
    assetId: 'xauusd', provider: 'tiingo', symbol: 'XAU/USD',
    price, priceKind: 'mid', bid: price - 0.05, ask: price + 0.05,
    dayVolume: null, providerTimestamp: '2026-10-28T20:00:00Z',
    receivedAt: '2026-10-28T20:00:00Z', status: 'fresh',
  },
});

describe('useLiveStream', () => {
  it('connects, applies valid frames to the query cache, and ignores malformed ones', () => {
    const { result } = renderHook(() => useLiveStream('xauusd', true), { wrapper });
    expect(result.current).toBe('connecting');
    const ws = FakeWebSocket.instances[0];
    expect(ws.url).toBe('ws://unit.test:8080/api/market/live/stream');

    act(() => ws.simulateOpen());
    expect(result.current).toBe('open');

    // Garbage and schema-invalid frames are dropped, never applied.
    act(() => {
      ws.simulateMessage('not json {{{');
      ws.simulateMessage({ type: 'quote', quote: { price: 'NaN' } });
      ws.simulateMessage({ type: 'bogus' });
    });
    expect(qc.getQueryData(['liveQuote', 'xauusd'])).toBeUndefined();

    act(() => ws.simulateMessage(quoteFrame(4065.04)));
    expect(qc.getQueryData(['liveQuote', 'xauusd'])).toMatchObject({ price: 4065.04, priceKind: 'mid' });
  });

  it('reports reconnecting on close and recovers the stream after backoff', () => {
    const { result } = renderHook(() => useLiveStream('xauusd', true), { wrapper });
    const ws = FakeWebSocket.instances[0];
    act(() => ws.simulateOpen());
    expect(result.current).toBe('open');

    act(() => ws.simulateClose());
    // During backoff the badge must not claim the feed is still open.
    expect(result.current).toBe('reconnecting');
    expect(FakeWebSocket.instances).toHaveLength(1);

    act(() => vi.advanceTimersByTime(3000));
    expect(FakeWebSocket.instances).toHaveLength(2);
    act(() => FakeWebSocket.instances[1].simulateOpen());
    expect(result.current).toBe('open');

    act(() => FakeWebSocket.instances[1].simulateMessage(quoteFrame(4061.5)));
    expect(qc.getQueryData(['liveQuote', 'xauusd'])).toMatchObject({ price: 4061.5 });
  });

  it('cleans up on unmount — closes the socket and never reconnects', () => {
    const { unmount } = renderHook(() => useLiveStream('xauusd', true), { wrapper });
    const ws = FakeWebSocket.instances[0];
    act(() => ws.simulateOpen());
    act(() => ws.simulateClose());
    unmount();
    act(() => vi.advanceTimersByTime(30000));
    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(ws.closed).toBe(true);
  });
});
