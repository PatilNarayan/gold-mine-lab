/**
 * Realtime client contract for the backend WebSocket feed.
 *
 * Intended channels (Phase 2+):
 *   - `market.candles`     live candle updates per asset/timeframe
 *   - `strategy.signals`   new strategy signals as they are computed
 *   - `backtest.progress`  progress events while a run executes
 *
 * Message envelope carried on every event:
 *   `{ type, channel, timestamp, payload }`
 *
 * Phase 1 ships only `NoopRealtimeClient`: it reports status "disabled" and
 * never opens a socket.
 */

export type RealtimeChannel = 'market.candles' | 'strategy.signals' | 'backtest.progress';

export type RealtimeStatus =
  | 'disabled'
  | 'connecting'
  | 'connected'
  | 'reconnecting'
  | 'disconnected'
  | 'error';

export interface RealtimeEnvelope<T = unknown> {
  type: string;
  channel: RealtimeChannel;
  timestamp: string; // ISO-8601 UTC
  payload: T;
}

export type RealtimeHandler = (msg: RealtimeEnvelope) => void;
export type StatusHandler = (status: RealtimeStatus) => void;

export interface RealtimeClient {
  readonly status: RealtimeStatus;
  connect(): void;
  disconnect(): void;
  subscribe(channel: RealtimeChannel, handler: RealtimeHandler): () => void;
  onStatusChange(handler: StatusHandler): () => void;
}

export class NoopRealtimeClient implements RealtimeClient {
  readonly status: RealtimeStatus = 'disabled';
  private statusHandlers = new Set<StatusHandler>();

  connect(): void {
    // Intentionally a no-op in Phase 1.
  }

  disconnect(): void {
    // Intentionally a no-op in Phase 1.
  }

  subscribe(_channel: RealtimeChannel, _handler: RealtimeHandler): () => void {
    return () => undefined;
  }

  onStatusChange(handler: StatusHandler): () => void {
    this.statusHandlers.add(handler);
    return () => this.statusHandlers.delete(handler);
  }
}
