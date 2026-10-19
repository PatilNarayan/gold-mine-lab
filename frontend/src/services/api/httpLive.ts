import type { LiveCandles, LiveQuote, LiveStatus, Timeframe } from '@/types/domain';
import { liveCandlesSchema, liveQuoteSchema, liveStatusSchema } from '@/types/domain';
import type { LiveMarketService } from '@/services/interfaces';
import { apiClient } from './client';

/** Thin pass-through to the backend's live endpoints (api.twelvedata on the server). */
export const httpLiveService: LiveMarketService = {
  getStatus(): Promise<LiveStatus> {
    return apiClient.get('/api/market/live/status', liveStatusSchema);
  },
  getQuote(assetId: string): Promise<LiveQuote> {
    return apiClient.get(`/api/market/live/quote?asset=${encodeURIComponent(assetId)}`, liveQuoteSchema);
  },
  getCandles(assetId: string, timeframe: Timeframe, limit = 200): Promise<LiveCandles> {
    return apiClient.get(
      `/api/market/live/candles?asset=${encodeURIComponent(assetId)}&timeframe=${timeframe}&limit=${limit}`,
      liveCandlesSchema,
    );
  },
};
