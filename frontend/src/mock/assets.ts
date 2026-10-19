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

// Demo mode is XAUUSD-only — same single-asset policy as the real API.
// Demo prices are generated, never presented as market data.
export const MOCK_ASSETS: Asset[] = [
  { id: 'xauusd', symbol: 'XAUUSD', name: 'Gold Spot (Demo)', assetClass: 'metal', quoteCurrency: 'USD' },
];

export const MOCK_ASSET_META: Record<string, MockAssetMeta> = {
  xauusd: { basePrice: 2320, digits: 2, volatility: 0.0075, baseVolume: 185_000 },
};

export function assetById(id: string): Asset | undefined {
  return MOCK_ASSETS.find((a) => a.id === id);
}

export function assetMeta(id: string): MockAssetMeta {
  return MOCK_ASSET_META[id] ?? { basePrice: 100, digits: 2, volatility: 0.01, baseVolume: 100_000 };
}
