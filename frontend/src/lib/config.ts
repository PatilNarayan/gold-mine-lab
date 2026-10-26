/**
 * Runtime configuration. `VITE_DATA_MODE` selects the service implementations;
 * API/WS base URLs come from env and can be overridden in Settings (persisted
 * to localStorage — non-sensitive preferences only).
 */

export type DataMode = 'mock' | 'api';

// Real data is the default — mock services only exist for explicit
// development/testing opt-in and are never selected silently.
export const dataMode: DataMode = import.meta.env.VITE_DATA_MODE === 'mock' ? 'mock' : 'api';

export const isMockMode = dataMode === 'mock';

const LS_API_URL = 'gml.apiUrl';
const LS_WS_URL = 'gml.wsUrl';

export function getApiUrl(): string {
  return localStorage.getItem(LS_API_URL) ?? import.meta.env.VITE_API_URL ?? 'http://localhost:8080';
}

export function getWsUrl(): string {
  return localStorage.getItem(LS_WS_URL) ?? import.meta.env.VITE_WS_URL ?? 'ws://localhost:8080/ws';
}

export function setApiUrlOverride(url: string | null): void {
  if (url) localStorage.setItem(LS_API_URL, url);
  else localStorage.removeItem(LS_API_URL);
}

export function setWsUrlOverride(url: string | null): void {
  if (url) localStorage.setItem(LS_WS_URL, url);
  else localStorage.removeItem(LS_WS_URL);
}
