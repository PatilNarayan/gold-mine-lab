import type { Asset, CandleSeries, MarketSummary, Timeframe } from '@/types/domain';
import type { MarketService } from '@/services/interfaces';
import { MOCK_ASSETS, assetById } from '@/mock/assets';
import { generateCandleSeries } from '@/mock/candles';
import { simulateDelay, isEmptyScenario } from './scenario';
import { ApiError } from '@/types/api';
import { round } from '@/mock/seed';

export const mockMarketService: MarketService = {
  async getAssets(): Promise<Asset[]> {
    await simulateDelay();
    return isEmptyScenario() ? [] : MOCK_ASSETS;
  },

  async getCandles(assetId: string, timeframe: Timeframe): Promise<CandleSeries> {
    await simulateDelay();
    if (isEmptyScenario()) {
      return { assetId, timeframe, candles: [], overlays: { ema20: [], ema50: [] } };
    }
    return generateCandleSeries(assetId, timeframe);
  },

  async getMarketSummary(assetId: string, timeframe: Timeframe): Promise<MarketSummary> {
    await simulateDelay();
    const asset = assetById(assetId);
    if (!asset) throw new ApiError('not_found', `Unknown asset "${assetId}"`, 'mock');
    const series = generateCandleSeries(assetId, timeframe);
    const candles = series.candles;
    const last = candles[candles.length - 1];
    const prev = candles[candles.length - 2] ?? last;
    const change = round(last.close - prev.close, 4);
    return {
      asset,
      timeframe,
      lastClose: last.close,
      change,
      changePercent: prev.close !== 0 ? round(change / prev.close, 4) : 0,
      asOf: last.time,
      status: 'Historical / Demo',
    };
  },
};
