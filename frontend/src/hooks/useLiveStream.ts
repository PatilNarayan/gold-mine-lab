import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { getApiUrl, isMockMode } from '@/lib/config';
import { liveStreamEventSchema, type LiveStreamEvent, type LiveCandles } from '@/types/domain';
import { mergeLiveBar } from '@/lib/liveBars';

export type StreamState = 'connecting' | 'open' | 'reconnecting' | 'closed';

const LIVE_CANDLES_LIMIT = 200;

/**
 * One managed WebSocket to the backend's live event stream. Provider ticks
 * become TanStack Query cache writes — the chart reads the same query keys
 * the REST bootstrap populated, so a tick flows through unchanged components.
 * Reconnects with bounded backoff; a single socket per mount, cleaned up on
 * unmount so no orphan listeners survive.
 */
export function useLiveStream(assetId: string, enabled: boolean): StreamState {
  const qc = useQueryClient();
  const [state, setState] = useState<StreamState>('closed');

  useEffect(() => {
    // Mock mode has no backend — connecting would only produce a reconnect loop.
    if (!enabled || isMockMode) {
      setState('closed');
      return;
    }
    let ws: WebSocket | null = null;
    let attempts = 0;
    let closed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const wsUrl = `${getApiUrl().replace(/^http/, 'ws')}/api/market/live/stream`;

    const connect = () => {
      if (closed) return;
      setState(attempts === 0 ? 'connecting' : 'reconnecting');
      ws = new WebSocket(wsUrl);
      ws.onopen = () => {
        attempts = 0;
        setState('open');
      };
      ws.onmessage = (msg) => {
        let raw: unknown;
        try {
          raw = JSON.parse(String(msg.data));
        } catch {
          return;
        }
        const parsed = liveStreamEventSchema.safeParse(raw);
        if (parsed.success) applyEvent(qc, assetId, parsed.data);
      };
      ws.onclose = () => {
        if (closed) return;
        attempts++;
        // 1s → ~15s bounded backoff with jitter.
        const delay = Math.min(15000, 1000 * 2 ** Math.min(attempts, 4)) + Math.random() * 500;
        timer = setTimeout(connect, delay);
      };
      ws.onerror = () => {
        ws?.close();
      };
    };
    connect();

    return () => {
      closed = true;
      clearTimeout(timer);
      setState('closed');
      ws?.close();
    };
  }, [assetId, enabled, qc]);

  return state;
}

function applyEvent(qc: ReturnType<typeof useQueryClient>, assetId: string, ev: LiveStreamEvent) {
  switch (ev.type) {
    case 'status':
      qc.setQueryData(['liveStatus'], ev.status);
      break;
    case 'quote':
      qc.setQueryData(['liveQuote', assetId], ev.quote);
      break;
    case 'bars':
      for (const u of ev.updates) {
        qc.setQueryData(
          ['liveCandles', assetId, u.timeframe, LIVE_CANDLES_LIMIT],
          (prev: LiveCandles | undefined) => mergeLiveBar(prev, u, LIVE_CANDLES_LIMIT),
        );
      }
      break;
  }
}
