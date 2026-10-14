import { z } from 'zod';
import type { Asset, CandleSeries, MarketSummary, Timeframe } from '@/types/domain';
import { assetSchema, candleSeriesSchema, marketSummarySchema } from '@/types/domain';
import type { MarketService } from '@/services/interfaces';
import { apiClient } from './client';

/** Thin pass-through to the planned Go REST endpoints. */
export const httpMarketService: MarketService = {
  getAssets(): Promise<Asset[]> {
    return apiClient.get('/api/assets', z.array(assetSchema));
  },
  getCandles(assetId: string, timeframe: Timeframe): Promise<CandleSeries> {
    return apiClient.get(
      `/api/market/candles?asset=${encodeURIComponent(assetId)}&timeframe=${timeframe}`,
      candleSeriesSchema,
    );
  },
  getMarketSummary(assetId: string, timeframe: Timeframe): Promise<MarketSummary> {
    return apiClient.get(
      `/api/market/summary?asset=${encodeURIComponent(assetId)}&timeframe=${timeframe}`,
      marketSummarySchema,
    );
  },
};
