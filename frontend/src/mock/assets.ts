import type { Asset } from '@/types/domain';

/**
 * Synthetic asset universe. Extra mock-only metadata (basePrice, digits,
 * volatility) drives the candle generator and is NOT part of the API Asset
 * shape.
 */

export interface MockAssetMeta {
  basePrice: number;
  digits: number;
  volatility: number; // per-bar stdev as a fraction of price
  baseVolume: number;
}

export const MOCK_ASSETS: Asset[] = [
  { id: 'xauusd', symbol: 'XAUUSD', name: 'Gold Spot', assetClass: 'metal', quoteCurrency: 'USD' },
  { id: 'xagusd', symbol: 'XAGUSD', name: 'Silver Spot', assetClass: 'metal', quoteCurrency: 'USD' },
  { id: 'msft', symbol: 'MSFT', name: 'Microsoft Corp', assetClass: 'equity', quoteCurrency: 'USD' },
  { id: 'tsla', symbol: 'TSLA', name: 'Tesla Inc', assetClass: 'equity', quoteCurrency: 'USD' },
  { id: 'eurusd', symbol: 'EURUSD', name: 'Euro / US Dollar', assetClass: 'forex', quoteCurrency: 'USD' },
];

export const MOCK_ASSET_META: Record<string, MockAssetMeta> = {
  xauusd: { basePrice: 2320, digits: 2, volatility: 0.0075, baseVolume: 185_000 },
  xagusd: { basePrice: 29.4, digits: 3, volatility: 0.011, baseVolume: 62_000 },
  msft: { basePrice: 428, digits: 2, volatility: 0.009, baseVolume: 4_200_000 },
  tsla: { basePrice: 246, digits: 2, volatility: 0.019, baseVolume: 18_500_000 },
  eurusd: { basePrice: 1.0852, digits: 4, volatility: 0.0028, baseVolume: 310_000 },
};

export function assetById(id: string): Asset | undefined {
  return MOCK_ASSETS.find((a) => a.id === id);
}

export function assetMeta(id: string): MockAssetMeta {
  return MOCK_ASSET_META[id] ?? { basePrice: 100, digits: 2, volatility: 0.01, baseVolume: 100_000 };
}
