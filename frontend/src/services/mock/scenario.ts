import { ApiError } from '@/types/api';

/**
 * Mock scenario switches, driven by a query param so loading/empty/error UI
 * can be demonstrated without a backend:
 *   ?mock=error   -> every service call fails with a normalized ApiError
 *   ?mock=empty   -> collections and series come back empty
 *   ?mock=slow    -> latency raised to ~2.5s to show skeletons
 */
export type MockScenario = 'normal' | 'error' | 'empty' | 'slow';

export function currentScenario(): MockScenario {
  const v = new URLSearchParams(window.location.search).get('mock');
  return v === 'error' || v === 'empty' || v === 'slow' ? v : 'normal';
}

export function isEmptyScenario(): boolean {
  return currentScenario() === 'empty';
}

function latencyMs(): number {
  return currentScenario() === 'slow' ? 2200 + Math.random() * 600 : 200 + Math.random() * 400;
}

/** Simulates network latency and the forced-error scenario. */
export async function simulateDelay(): Promise<void> {
  const ms = latencyMs();
  await new Promise((r) => setTimeout(r, ms));
  if (currentScenario() === 'error') {
    throw new ApiError(
      'mock_forced_error',
      'Forced mock error scenario (?mock=error). Remove the query param to recover.',
      'mock-scenario',
    );
  }
}
