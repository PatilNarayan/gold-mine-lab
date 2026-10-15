import { executeBacktest } from '@/mock/backtests';
import { ApiError } from '@/types/api';
import type {
  CreateGraphRequest, GraphBacktestRequest, GraphDefinition, GraphFieldError,
  GraphRevision, StrategyGraph, ValidateGraphResponse, BacktestRun,
  GraphDiagnostics, GraphDiagnosticsRequest, GraphNodeSummary, GraphTraceRow,
} from '@/types/domain';
import type { GraphService } from '../interfaces';

interface StoredGraph extends StrategyGraph {
  defs: GraphRevision[];
}

const graphs = new Map<string, StoredGraph>();
let counter = 0;

const STARTER: GraphDefinition = {
  nodes: [
    { id: 'ema', type: 'strategy', strategyId: 'ema-cross', parameters: { fast_period: 12, slow_period: 26 } },
    { id: 'rsi', type: 'strategy', strategyId: 'rsi-mean-reversion' },
    { id: 'conf', type: 'and' },
    { id: 'out', type: 'output' },
  ],
  edges: [
    { from: 'ema', to: 'conf' },
    { from: 'rsi', to: 'conf' },
    { from: 'conf', to: 'out' },
  ],
};

function seed() {
  if (graphs.size === 0) {
    const id = 'graph-mock-1';
    graphs.set(id, {
      id, name: 'EMA + RSI confirmation', description: 'Starter template graph',
      status: 'active', revisions: 1, createdAt: new Date().toISOString(),
      defs: [{ graphId: id, revision: 1, definition: STARTER, definitionHash: 'mock-hash-1', createdAt: new Date().toISOString() }],
    });
  }
}

// Mirrors the backend validator's core rules so mock mode stays honest.
function validateLocal(d: GraphDefinition): GraphFieldError[] {
  const errs: GraphFieldError[] = [];
  const ids = new Set<string>();
  const inDeg = new Map<string, number>();
  for (const n of d.nodes) {
    if (ids.has(n.id)) errs.push({ nodeId: n.id, message: 'duplicate node id' });
    ids.add(n.id);
    if (n.type === 'strategy' && !n.strategyId) errs.push({ nodeId: n.id, message: 'strategy source requires strategyId' });
    if (n.type === 'threshold' && (n.threshold == null || n.threshold < 0 || n.threshold > 1))
      errs.push({ nodeId: n.id, message: 'threshold must be in [0,1]' });
  }
  for (const e of d.edges) {
    if (!ids.has(e.from)) errs.push({ nodeId: e.from, message: `edge references unknown source node "${e.from}"` });
    if (!ids.has(e.to)) errs.push({ nodeId: e.to, message: `edge references unknown target node "${e.to}"` });
    inDeg.set(e.to, (inDeg.get(e.to) ?? 0) + 1);
  }
  let outputs = 0;
  for (const n of d.nodes) {
    const deg = inDeg.get(n.id) ?? 0;
    if (n.type === 'strategy' && deg > 0) errs.push({ nodeId: n.id, message: 'strategy source must not have inputs' });
    if ((n.type === 'and' || n.type === 'or') && deg < 2) errs.push({ nodeId: n.id, message: `${n.type} node requires at least 2 inputs` });
    if (n.type === 'threshold' && deg !== 1) errs.push({ nodeId: n.id, message: 'threshold node requires exactly 1 input' });
    if (n.type === 'output') { outputs++; if (deg !== 1) errs.push({ nodeId: n.id, message: 'output node requires exactly 1 input' }); }
  }
  if (outputs === 0) errs.push({ message: 'graph has no output node' });
  if (outputs > 1) errs.push({ message: 'graph has multiple output nodes' });
  return errs;
}

export const mockGraphService: GraphService = {
  async list() { seed(); return [...graphs.values()].map(({ defs: _d, ...g }) => g); },
  async validate(definition) {
    seed();
    const errors = validateLocal(definition);
    const res: ValidateGraphResponse = { valid: errors.length === 0, errors };
    return res;
  },
  async create(req: CreateGraphRequest) {
    seed();
    const errors = validateLocal(req.definition);
    if (errors.length) throw new ApiError('invalid_request', errors.map((e) => e.message).join('; '), 'mock');
    const id = `graph-mock-${++counter + 1}`;
    const g: StoredGraph = {
      id, name: req.name, description: req.description, status: 'active', revisions: 1,
      createdAt: new Date().toISOString(),
      defs: [{ graphId: id, revision: 1, definition: req.definition, definitionHash: `mock-hash-${id}-1`, createdAt: new Date().toISOString() }],
    };
    graphs.set(id, g);
    const { defs: _d, ...out } = g;
    return out;
  },
  async get(id) {
    seed();
    const g = graphs.get(id);
    if (!g) throw new ApiError('not_found', `Unknown graph "${id}"`, 'mock');
    const { defs: _d, ...out } = g;
    return out;
  },
  async listRevisions(id) {
    seed();
    const g = graphs.get(id);
    if (!g) throw new ApiError('not_found', `Unknown graph "${id}"`, 'mock');
    return g.defs;
  },
  async createRevision(id, definition) {
    seed();
    const g = graphs.get(id);
    if (!g) throw new ApiError('not_found', `Unknown graph "${id}"`, 'mock');
    const errors = validateLocal(definition);
    if (errors.length) throw new ApiError('invalid_request', errors.map((e) => e.message).join('; '), 'mock');
    const rev: GraphRevision = {
      graphId: id, revision: g.defs.length + 1, definition,
      definitionHash: `mock-hash-${id}-${g.defs.length + 1}`, createdAt: new Date().toISOString(),
    };
    g.defs.push(rev);
    g.revisions = g.defs.length;
    return rev;
  },
  async runBacktest(id, revision, req: GraphBacktestRequest): Promise<BacktestRun> {
    seed();
    const g = graphs.get(id);
    if (!g || !g.defs.some((r) => r.revision === revision))
      throw new ApiError('not_found', 'Unknown graph revision', 'mock');
    // Delegate to the shared mock engine, then relabel provenance to the graph.
    const run = executeBacktest({ ...req, strategyId: 'ema-cross', parameters: {} });
    run.strategyId = `graph:${id}:v${revision}`;
    run.parameters = { graphId: id, graphRevision: revision };
    return run;
  },
  async diagnostics(id, revision, req: GraphDiagnosticsRequest): Promise<GraphDiagnostics> {
    seed();
    const g = graphs.get(id);
    const rev = g?.defs.find((r) => r.revision === revision);
    if (!g || !rev) throw new ApiError('not_found', 'Unknown graph revision', 'mock');
    // Deterministic synthetic replay mirroring the real inspector's shape:
    // every source leans neutral, operators inherit their combine reasons.
    const days = Math.max(1, Math.round((Date.parse(req.endDate) - Date.parse(req.startDate)) / 86400000));
    const limit = req.traceLimit && req.traceLimit > 0 ? Math.min(req.traceLimit, 2000) : 200;
    const mkCounts = (i: number) => {
      const buy = Math.floor(days * 0.01 * (i + 1));
      const sell = Math.floor(days * 0.008 * (i + 1));
      return { buy, sell, neutral: days - buy - sell };
    };
    const nodes: GraphNodeSummary[] = rev.definition.nodes.map((n, i) => {
      const c = mkCounts(i % 3);
      const actionable = c.buy + c.sell;
      const actionablePct = days > 0 ? (actionable / days) * 100 : 0;
      const reasons =
        n.type === 'and' ? [{ reason: 'and: input neutral', count: c.neutral }]
        : n.type === 'or' ? [{ reason: 'or: all inputs neutral', count: c.neutral }]
        : n.type === 'threshold' ? [{ reason: 'threshold: input neutral', count: c.neutral }]
        : n.type === 'output' ? [{ reason: 'and: input neutral', count: c.neutral }]
        : [{ reason: 'no crossover', count: c.neutral }, { reason: 'EMA12 crossed above EMA26', count: c.buy }];
      return {
        nodeId: n.id, nodeType: n.type, evaluatedBars: days,
        buy: c.buy, sell: c.sell, neutral: c.neutral, actionablePct,
        avgScore: actionable > 0 ? 0.5 : null, topReasons: reasons,
      };
    });
    const rows = Math.min(limit, 10);
    const traces: GraphTraceRow[] = Array.from({ length: rows }, (_, k) => ({
      timestamp: new Date(Date.parse(req.startDate) + k * 86400000).toISOString(),
      nodes: rev.definition.nodes.map((n, i) => ({
        nodeId: n.id,
        action: (k === 1 && i === 0 ? 'BUY' : 'NEUTRAL') as 'BUY' | 'NEUTRAL',
        score: k === 1 && i === 0 ? 0.6 : 0,
        reason: n.type === 'strategy' ? 'no crossover' : 'and: input neutral',
      })),
    }));
    return {
      graphId: id, revision, hash: rev.definitionHash,
      assetId: req.assetId, timeframe: req.timeframe,
      startDate: req.startDate, endDate: req.endDate,
      evaluatedBars: days, warmupBars: 60,
      dataset: 'mock:dataset@sha256:deadbeef', lastCandle: new Date(Date.parse(req.endDate)).toISOString(),
      traceLimit: limit, truncated: days > limit, nodes, traces,
    };
  },
};
