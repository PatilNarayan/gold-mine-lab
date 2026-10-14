import { create } from 'zustand';
import {
  getApiUrl,
  getWsUrl,
  setApiUrlOverride,
  setWsUrlOverride,
} from '@/lib/config';

interface SettingsState {
  apiUrl: string;
  wsUrl: string;
  setApiUrl: (url: string) => void;
  setWsUrl: (url: string) => void;
}

/**
 * API/WS endpoint preferences. Persisted to localStorage via config helpers
 * (non-sensitive only — URLs, never credentials).
 */
export const useSettingsStore = create<SettingsState>()((set) => ({
  apiUrl: getApiUrl(),
  wsUrl: getWsUrl(),
  setApiUrl: (url) => {
    setApiUrlOverride(url || null);
    set({ apiUrl: url });
  },
  setWsUrl: (url) => {
    setWsUrlOverride(url || null);
    set({ wsUrl: url });
  },
}));
