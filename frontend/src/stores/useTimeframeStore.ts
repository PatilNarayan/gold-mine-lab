import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { Timeframe } from '@/types/domain';
import { DEFAULT_TIMEFRAME } from '@/lib/constants';

interface TimeframeState {
  timeframe: Timeframe;
  setTimeframe: (timeframe: Timeframe) => void;
}

export const useTimeframeStore = create<TimeframeState>()(
  persist(
    (set) => ({
      timeframe: DEFAULT_TIMEFRAME,
      setTimeframe: (timeframe) => set({ timeframe }),
    }),
    { name: 'gml.timeframe' },
  ),
);
