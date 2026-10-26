import type { ShadowService } from '@/services/interfaces';
import type { ShadowStatus, ShadowOrders } from '@/types/domain';

/**
 * Mock shadow service — honest empty state, no fabricated trades. Mock mode
 * exists for tests/fixtures; a mock shadow engine would invent executions,
 * which is exactly what this UI must never show.
 */
const emptyStatus: ShadowStatus = {
  mode: 'simulated',
  strategyId: 'ema-cross',
  strategyVersion: '1.0.0',
  symbol: 'XAU/USD',
  timeframe: '1h',
  realizedPnl: 0,
  realizedCommissions: 0,
  realizedSpreadCost: 0,
  equity: 100000,
  orderStats: { filled: 0, expired: 0, rejected: 0, missed: 0, pending: 0 },
  session: { id: 0, provider: 'none', restartCount: 0 },
  metrics: {
    barsProcessed: 0, signalsSeen: 0, quotesSeen: 0, quotesRejected: 0,
    fills: 0, expired: 0, missed: 0, recoveries: 0, duplicateEventsSkipped: 0,
  },
};

export const mockShadowService: ShadowService = {
  getStatus(): Promise<ShadowStatus> {
    return Promise.resolve(emptyStatus);
  },
  getOrders(): Promise<ShadowOrders> {
    return Promise.resolve({ mode: 'simulated', orders: [] });
  },
};
