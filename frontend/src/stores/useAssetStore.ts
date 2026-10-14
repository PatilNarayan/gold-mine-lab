import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { DEFAULT_ASSET_ID } from '@/lib/constants';

interface AssetState {
  assetId: string;
  setAssetId: (assetId: string) => void;
}

/** Selected asset — UI state only; asset data itself comes from queries. */
export const useAssetStore = create<AssetState>()(
  persist(
    (set) => ({
      assetId: DEFAULT_ASSET_ID,
      setAssetId: (assetId) => set({ assetId }),
    }),
    { name: 'gml.asset' },
  ),
);
