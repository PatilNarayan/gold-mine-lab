import type {
  BacktestService,
  LiveMarketService,
  SignalResearchService,
  ResearchStudyService,
  GraphService,
  ComparisonService,
  WalkForwardService,
  HealthService,
  MarketService,
  StrategyService,
  TradeService,
} from './interfaces';
import type { RealtimeClient } from './websocket/realtime';
import { NoopRealtimeClient } from './websocket/realtime';
import { dataMode, type DataMode } from '@/lib/config';

import { mockMarketService } from './mock/mockMarket';
import { mockStrategyService } from './mock/mockStrategy';
import { mockBacktestService } from './mock/mockBacktest';
import { mockTradeService } from './mock/mockTrade';
import { mockHealthService } from './mock/mockHealth';

import { httpMarketService } from './api/httpMarket';
import { httpLiveService } from './api/httpLive';
import { mockLiveService } from './mock/mockLive';
import { httpStrategyService } from './api/httpStrategy';
import { httpBacktestService } from './api/httpBacktest';
import { httpTradeService } from './api/httpTrade';
import { httpHealthService } from './api/httpHealth';
import { httpComparisonService } from './api/httpComparison';
import { mockComparisonService } from './mock/mockComparison';
import { httpWalkForwardService } from './api/httpWalkForward';
import { mockWalkForwardService } from './mock/mockWalkForward';
import { httpGraphService } from './api/httpStrategyGraph';
import { mockGraphService } from './mock/mockStrategyGraph';
import { httpSignalResearchService } from './api/httpSignalResearch';
import { mockSignalResearchService } from './mock/mockSignalResearch';
import { httpResearchStudyService } from './api/httpResearchStudies';
import { mockResearchStudyService } from './mock/mockResearchStudies';

export interface Services {
  market: MarketService;
  live: LiveMarketService;
  strategy: StrategyService;
  backtest: BacktestService;
  trade: TradeService;
  comparison: ComparisonService;
  walkForward: WalkForwardService;
  graph: GraphService;
  signalResearch: SignalResearchService;
  researchStudies: ResearchStudyService;
  health: HealthService;
  realtime: RealtimeClient;
}

/**
 * Service factory. `VITE_DATA_MODE=mock` (default) wires deterministic mock
 * implementations; `api` wires HTTP implementations against the Go backend.
 * UI code only sees the `Services` interfaces.
 */
export function createServices(mode: DataMode = dataMode): Services {
  if (mode === 'api') {
    return {
      market: httpMarketService,
      live: httpLiveService,
      strategy: httpStrategyService,
      backtest: httpBacktestService,
      trade: httpTradeService,
      comparison: httpComparisonService,
      walkForward: httpWalkForwardService,
      graph: httpGraphService,
      signalResearch: httpSignalResearchService,
      researchStudies: httpResearchStudyService,
      health: httpHealthService,
      realtime: new NoopRealtimeClient(),
    };
  }
  return {
    market: mockMarketService,
    live: mockLiveService,
    strategy: mockStrategyService,
    backtest: mockBacktestService,
    trade: mockTradeService,
    comparison: mockComparisonService,
    walkForward: mockWalkForwardService,
    graph: mockGraphService,
    signalResearch: mockSignalResearchService,
    researchStudies: mockResearchStudyService,
    health: mockHealthService,
    realtime: new NoopRealtimeClient(),
  };
}

/** Shared singleton used by hooks/components. */
export const services = createServices();

export type { RealtimeClient, RealtimeEnvelope, RealtimeStatus, RealtimeChannel } from './websocket/realtime';
export * from './interfaces';
