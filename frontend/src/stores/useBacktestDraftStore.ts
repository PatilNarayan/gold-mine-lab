import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { BacktestFormValues } from '@/lib/backtestForm';

export type BacktestDraft = Partial<BacktestFormValues>;

interface BacktestDraftState {
  draft: BacktestDraft;
  update: (patch: BacktestDraft) => void;
  reset: () => void;
}

/** Form draft for the Backtests page (UI state, safe to persist). */
export const useBacktestDraftStore = create<BacktestDraftState>()(
  persist(
    (set) => ({
      draft: {},
      update: (patch) => set((s) => ({ draft: { ...s.draft, ...patch } })),
      reset: () => set({ draft: {} }),
    }),
    { name: 'gml.backtestDraft' },
  ),
);
