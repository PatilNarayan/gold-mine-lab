import { describe, expect, it } from 'vitest';
import { createServices } from './index';
import { mockMarketService } from './mock/mockMarket';
import { mockHealthService } from './mock/mockHealth';
import { httpMarketService } from './api/httpMarket';
import { httpHealthService } from './api/httpHealth';
import { NoopRealtimeClient } from './websocket/realtime';

describe('service factory', () => {
  it('selects mock implementations in mock mode', () => {
    const s = createServices('mock');
    expect(s.market).toBe(mockMarketService);
    expect(s.health).toBe(mockHealthService);
    expect(s.realtime).toBeInstanceOf(NoopRealtimeClient);
    expect(s.realtime.status).toBe('disabled');
  });

  it('selects http implementations in api mode', () => {
    const s = createServices('api');
    expect(s.market).toBe(httpMarketService);
    expect(s.health).toBe(httpHealthService);
  });

  it('never opens a socket in Phase 1', () => {
    for (const mode of ['mock', 'api'] as const) {
      const s = createServices(mode);
      expect(s.realtime.status).toBe('disabled');
      expect(() => s.realtime.connect()).not.toThrow();
      expect(() => s.realtime.disconnect()).not.toThrow();
    }
  });

  it('mock services resolve data end-to-end', async () => {
    const s = createServices('mock');
    const assets = await s.market.getAssets();
    expect(assets.length).toBeGreaterThan(0);
    const health = await s.health.check();
    expect(health.status).toBe('ok');
  });
});
