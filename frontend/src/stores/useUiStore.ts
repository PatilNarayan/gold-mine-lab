import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export type Theme = 'dark' | 'light';

interface UiState {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  mobileNavOpen: boolean;
  setMobileNavOpen: (open: boolean) => void;
}

/** Chrome-level UI state. Only the theme persists. */
export const useUiStore = create<UiState>()(
  persist(
    (set) => ({
      theme: 'dark',
      setTheme: (theme) => set({ theme }),
      mobileNavOpen: false,
      setMobileNavOpen: (open) => set({ mobileNavOpen: open }),
    }),
    { name: 'gml.ui', partialize: (s) => ({ theme: s.theme }) },
  ),
);
