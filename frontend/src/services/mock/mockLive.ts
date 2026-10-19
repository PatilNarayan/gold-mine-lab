import type { LiveCandles, LiveQuote, LiveStatus, Timeframe } from '@/types/domain';
import type { LiveMarketService } from '@/services/interfaces';
import { simulateDelay } from './scenario';
import { ApiError } from '@/types/api';

/**
 * Mock mode never fabricates live data: it reports the feature as disabled so
 * the UI exercises its unavailable state truthfully instead of showing fake
 * prices.
 */
export const mockLiveService: LiveMarketService = {
  async getStatus(): Promise<LiveStatus> {
    await simulateDelay();
    return {
      enabled: false,
      state: 'disabled',
      provider: 'twelvedata',
      symbol: 'XAU/USD',
      lastQuoteAt: null,
      lastError: 'mock mode — live data is not simulated',
      staleAfterSeconds: 30,
    };
  },
  async getQuote(_assetId: string): Promise<LiveQuote> {
    await simulateDelay();
    throw new ApiError('unavailable', 'live data is not simulated in mock mode', 'mock');
  },
  async getCandles(_assetId: string, _timeframe: Timeframe): Promise<LiveCandles> {
    await simulateDelay();
    throw new ApiError('unavailable', 'live data is not simulated in mock mode', 'mock');
  },
};
