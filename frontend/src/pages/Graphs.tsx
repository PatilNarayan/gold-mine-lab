import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ReactFlow, Background, Controls, addEdge, applyNodeChanges, applyEdgeChanges,
  type Connection, type Edge, type EdgeChange, type Node, type NodeChange } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { DemoBanner } from '@/components/common/DemoBanner';
import { useGraphs, useGraph, useGraphRevisions, useCreateGraph, useCreateRevision,
  useValidateGraph, useRunGraphBacktest, useGraphDiagnostics } from '@/hooks/useGraphs';
import { useStrategies } from '@/hooks/useStrategies';
import type { GraphDefinition, GraphEdge, GraphNode } from '@/types/domain';
import { formatDateTime } from '@/lib/formatters';

const SOURCE_PARAMS: Record<string, { key: string; label: string; def: number }[]> = {
  'ema-cross': [
    { key: 'fast_period', label: 'Fast EMA', def: 20 },
    { key: 'slow_period', label: 'Slow EMA', def: 50 },
  ],
  'rsi-mean-reversion': [
    { key: 'rsi_period', label: 'RSI period', def: 14 },
    { key: 'oversold', label: 'Oversold', def: 30 },
    { key: 'overbought', label: 'Overbought', def: 70 },
  ],
};

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

const NODE_LABEL: Record<GraphNode['type'], string> = {
  strategy: 'STRATEGY', and: 'AND', or: 'OR', threshold: 'THRESHOLD', output: 'OUTPUT',
};

function nodeLabel(n: GraphNode): string {
  if (n.type === 'strategy') return `${n.strategyId}`;
  if (n.type === 'threshold') return `threshold ≥ ${n.threshold ?? 0}`;
  return NODE_LABEL[n.type];
}

// Deterministic layered layout: depth = longest path from a source.
function layout(def: GraphDefinition): Map<string, { x: number; y: number }> {
  const depth = new Map<string, number>();
  const memo = (id: string, seen: Set<string>): number => {
    if (depth.has(id)) return depth.get(id)!;
    if (seen.has(id)) return 0;
    seen.add(id);
    const ins = def.edges.filter((e) => e.to === id);
    const d = ins.length === 0 ? 0 : 1 + Math.max(...ins.map((e) => memo(e.from, seen)));
    depth.set(id, d);
    return d;
  };
  for (const n of def.nodes) memo(n.id, new Set());
  const pos = new Map<string, { x: number; y: number }>();
  const perDepth = new Map<number, number>();
  for (const n of def.nodes) {
    const d = depth.get(n.id) ?? 0;
    const row = perDepth.get(d) ?? 0;
    perDepth.set(d, row + 1);
    pos.set(n.id, { x: 80 + d * 240, y: 60 + row * 110 });
  }
  return pos;
}

function toFlow(def: GraphDefinition): { nodes: Node[]; edges: Edge[] } {
  const pos = layout(def);
  return {
    nodes: def.nodes.map((n) => ({
      id: n.id, position: pos.get(n.id)!,
      data: { label: `${nodeLabel(n)}\n${NODE_LABEL[n.type]}` },
      type: 'default',
    })),
    edges: def.edges.map((e) => ({ id: `${e.from}->${e.to}`, source: e.from, target: e.to })),
  };
}

function fromFlow(nodes: Node[], edges: Edge[], defNodes: GraphNode[]): GraphDefinition {
  return {
    nodes: defNodes.filter((n) => nodes.some((fn) => fn.id === n.id)),
    edges: edges.map((e): GraphEdge => ({ from: e.source, to: e.target })),
  };
}

let nodeCounter = 100;

export default function Graphs() {
  const { id } = useParams();
  return id ? <GraphDetail id={id} /> : <GraphList />;
}

function GraphList() {
  const graphs = useGraphs();
  const create = useCreateGraph();
  const navigate = useNavigate();
  return (
    <div className="space-y-4">
      <DemoBanner />
      <section className="card space-y-3">
        <div className="flex items-center justify-between">
          <h1 className="text-sm font-semibold text-content-1">Strategy graphs</h1>
          <button
            className="btn"
            disabled={create.isPending}
            onClick={() =>
              create.mutate(
                { name: 'EMA ∧ RSI confirmation', description: 'Starter template — an example, not a recommended strategy.', definition: STARTER },
                { onSuccess: (g) => navigate(`/graphs/${g.id}`) },
              )
            }
          >
            + New graph (EMA∧RSI template)
          </button>
        </div>
        {create.isError && <p className="text-xs text-red-400">{(create.error as Error).message}</p>}
        <table className="w-full text-sm">
          <thead><tr className="text-left text-xs text-content-3">
            {['Name', 'Revisions', 'Status', 'Created'].map((h) => <th key={h} className="py-1 pr-3">{h}</th>)}
          </tr></thead>
          <tbody>
            {(graphs.data ?? []).map((g) => (
              <tr key={g.id} className="border-t border-line cursor-pointer hover:bg-surface-2" onClick={() => navigate(`/graphs/${g.id}`)}>
                <td className="py-1.5 pr-3">{g.name}<span className="ml-2 text-2xs text-content-3 tnum">{g.id.slice(0, 8)}</span></td>
                <td className="tnum pr-3">{g.revisions}</td>
                <td className="pr-3">{g.status}</td>
                <td className="text-content-3">{formatDateTime(g.createdAt)}</td>
              </tr>
            ))}
            {graphs.data?.length === 0 && (
              <tr><td colSpan={4} className="py-3 text-content-3">No graphs yet — create one from the starter template.</td></tr>
            )}
          </tbody>
        </table>
      </section>
      <p className="text-xs text-content-3">
        Graphs combine existing strategies with explicit AND/OR/threshold rules and feed one composite
        signal into the standard backtester. Illustrative for research only; no trades are executed.
      </p>
    </div>
  );
}

function GraphDetail({ id }: { id: string }) {
  const graph = useGraph(id);
  const revisions = useGraphRevisions(id);
  const strategies = useStrategies();
  const createRev = useCreateRevision(id);
  const validate = useValidateGraph();
  const navigate = useNavigate();

  const [revIdx, setRevIdx] = useState<number | null>(null);
  const [defNodes, setDefNodes] = useState<GraphNode[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [selNode, setSelNode] = useState<string | null>(null);
  const [verr, setVerr] = useState<string[]>([]);

  const current = useMemo(() => {
    const vs = revisions.data ?? [];
    return vs[revIdx ?? vs.length - 1];
  }, [revisions.data, revIdx]);

  useEffect(() => {
    if (!current) return;
    setDefNodes(current.definition.nodes);
    const f = toFlow(current.definition);
    setNodes(f.nodes);
    setEdges(f.edges);
    setSelNode(null);
    setVerr([]);
  }, [current]);

  const workingDef = useMemo(() => fromFlow(nodes, edges, defNodes), [nodes, edges, defNodes]);
  const run = useRunGraphBacktest(id, current?.revision ?? 0);
  const [runForm, setRunForm] = useState({
    timeframe: '1d', startDate: '2020-01-01', endDate: '2021-06-30',
    initialCapital: 100000, positionSize: 10, commission: 0.02, slippage: 0.01,
  });

  const onNodesChange = useCallback((cs: NodeChange[]) => setNodes((ns) => applyNodeChanges(cs, ns)), []);
  const onEdgesChange = useCallback((cs: EdgeChange[]) => setEdges((es) => applyEdgeChanges(cs, es)), []);
  const onConnect = useCallback((c: Connection) => setEdges((es) => addEdge({ ...c, id: `${c.source}->${c.target}` }, es)), []);

  const addNode = (type: GraphNode['type'], strategyId?: string) => {
    const nid = `${type === 'strategy' ? strategyId : type}-${++nodeCounter}`;
    const gn: GraphNode = { id: nid, type };
    if (type === 'strategy') {
      gn.strategyId = strategyId;
      gn.parameters = Object.fromEntries((SOURCE_PARAMS[strategyId ?? ''] ?? []).map((p) => [p.key, p.def]));
    }
    if (type === 'threshold') gn.threshold = 0.5;
    setDefNodes((ns) => [...ns, gn]);
    setNodes((ns) => [...ns, { id: nid, position: { x: 120 + Math.random() * 200, y: 120 + Math.random() * 160 }, data: { label: nodeLabel(gn) }, type: 'default' }]);
  };

  const updateNode = (nid: string, patch: Partial<GraphNode>) => {
    setDefNodes((ns) => ns.map((n) => (n.id === nid ? { ...n, ...patch } : n)));
    setNodes((ns) => ns.map((n) => (n.id === nid ? { ...n, data: { label: nodeLabel({ ...defNodes.find((d) => d.id === nid)!, ...patch }) } } : n)));
  };

  const doValidate = () => {
    validate.mutate(workingDef, {
      onSuccess: (r) => setVerr(r.valid ? [] : r.errors.map((e) => (e.nodeId ? `${e.nodeId}: ${e.message}` : e.message))),
    });
  };

  const sel = selNode ? defNodes.find((n) => n.id === selNode) : undefined;
  const g = graph.data;

  return (
    <div className="space-y-4">
      <DemoBanner />
      <button className="text-xs text-content-3 hover:text-content-1" onClick={() => navigate('/graphs')}>← All graphs</button>

      <section className="card space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-sm font-semibold text-content-1">{g?.name ?? 'Graph'}</h1>
          <span className="text-2xs text-content-3 tnum">{id}</span>
          <span className="text-2xs text-content-3">
            revision <select className="bg-surface-2 border border-line px-1" value={revIdx ?? (revisions.data?.length ?? 1) - 1}
              onChange={(e) => setRevIdx(Number(e.target.value))}>
              {(revisions.data ?? []).map((r, i) => <option key={r.revision} value={i}>v{r.revision} · {r.definitionHash.slice(0, 8)}</option>)}
            </select>
          </span>
        </div>
        {g?.description && <p className="text-xs text-content-3">{g.description}</p>}
      </section>

      <section className="card space-y-2" aria-label="Graph editor">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="section-label">Add:</span>
          {(strategies.data ?? []).filter((s) => SOURCE_PARAMS[s.id]).map((s) => (
            <button key={s.id} className="btn text-xs" onClick={() => addNode('strategy', s.id)}>+ {s.id}</button>
          ))}
          <button className="btn text-xs" onClick={() => addNode('and')}>+ AND</button>
          <button className="btn text-xs" onClick={() => addNode('or')}>+ OR</button>
          <button className="btn text-xs" onClick={() => addNode('threshold')}>+ Threshold</button>
          <button className="btn text-xs" onClick={() => addNode('output')}>+ Output</button>
          <span className="text-content-3">Drag from a node's edge to connect · select + Delete to remove</span>
        </div>
        <div className="h-80 border border-line">
          <ReactFlow
            nodes={nodes} edges={edges}
            onNodesChange={onNodesChange} onEdgesChange={onEdgesChange} onConnect={onConnect}
            onNodeClick={(_, n) => setSelNode(n.id)}
            onPaneClick={() => setSelNode(null)}
            deleteKeyCode={['Delete', 'Backspace']} fitView colorMode="dark"
            proOptions={{ hideAttribution: true }}
          >
            <Background />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
        {sel && (
          <div className="border border-line bg-surface-1 p-2 text-xs space-y-2">
            <div className="section-label">Node {sel.id} · {NODE_LABEL[sel.type]}</div>
            {sel.type === 'strategy' && (
              <div className="flex flex-wrap gap-3">
                {(SOURCE_PARAMS[sel.strategyId ?? ''] ?? []).map((p) => (
                  <label key={p.key} className="flex items-center gap-1">
                    {p.label}
                    <input type="number" className="w-16 bg-surface-2 border border-line px-1 tnum"
                      value={Number((sel.parameters ?? {})[p.key] ?? p.def)}
                      onChange={(e) => updateNode(sel.id, { parameters: { ...(sel.parameters ?? {}), [p.key]: Number(e.target.value) } })} />
                  </label>
                ))}
              </div>
            )}
            {sel.type === 'threshold' && (
              <label className="flex items-center gap-1">
                Threshold [0,1]
                <input type="number" step="0.05" min="0" max="1" className="w-20 bg-surface-2 border border-line px-1 tnum"
                  value={sel.threshold ?? 0.5}
                  onChange={(e) => updateNode(sel.id, { threshold: Number(e.target.value) })} />
              </label>
            )}
          </div>
        )}
        <div className="flex items-center gap-2">
          <button className="btn" onClick={doValidate} disabled={validate.isPending}>Validate</button>
          <button className="btn" disabled={createRev.isPending}
            onClick={() => createRev.mutate(workingDef, { onSuccess: () => setRevIdx((revisions.data?.length ?? 0)) })}>
            Save as new revision
          </button>
          {createRev.isError && <span className="text-xs text-red-400">{(createRev.error as Error).message}</span>}
          {validate.data?.valid && verr.length === 0 && <span className="text-xs text-green-400">valid</span>}
        </div>
        {verr.length > 0 && (
          <ul className="text-xs text-red-400 space-y-0.5" role="alert">
            {verr.map((e, i) => <li key={i}>• {e}</li>)}
          </ul>
        )}
      </section>

      <section className="card space-y-2" aria-label="Run backtest">
        <h3 className="section-label">RUN BACKTEST — revision {current?.revision}</h3>
        <div className="flex flex-wrap items-end gap-3 text-xs">
          <label>TF <select className="bg-surface-2 border border-line px-1" value={runForm.timeframe}
            onChange={(e) => setRunForm({ ...runForm, timeframe: e.target.value })}>
            {['1h', '4h', '1d'].map((t) => <option key={t}>{t}</option>)}</select></label>
          <label>Start <input type="date" className="bg-surface-2 border border-line px-1" value={runForm.startDate}
            onChange={(e) => setRunForm({ ...runForm, startDate: e.target.value })} /></label>
          <label>End <input type="date" className="bg-surface-2 border border-line px-1" value={runForm.endDate}
            onChange={(e) => setRunForm({ ...runForm, endDate: e.target.value })} /></label>
          <label>Capital <input type="number" className="w-24 bg-surface-2 border border-line px-1 tnum" value={runForm.initialCapital}
            onChange={(e) => setRunForm({ ...runForm, initialCapital: Number(e.target.value) })} /></label>
          <label>Size % <input type="number" className="w-16 bg-surface-2 border border-line px-1 tnum" value={runForm.positionSize}
            onChange={(e) => setRunForm({ ...runForm, positionSize: Number(e.target.value) })} /></label>
          <label>Comm % <input type="number" step="0.01" className="w-16 bg-surface-2 border border-line px-1 tnum" value={runForm.commission}
            onChange={(e) => setRunForm({ ...runForm, commission: Number(e.target.value) })} /></label>
          <label>Slip % <input type="number" step="0.01" className="w-16 bg-surface-2 border border-line px-1 tnum" value={runForm.slippage}
            onChange={(e) => setRunForm({ ...runForm, slippage: Number(e.target.value) })} /></label>
          <button className="btn" disabled={run.isPending || !current}
            onClick={() => run.mutate({ assetId: 'xauusd', ...runForm, timeframe: runForm.timeframe as never },
              { onSuccess: (r) => navigate(`/results/${r.id}`) })}>
            {run.isPending ? 'Running…' : 'Run backtest'}
          </button>
        </div>
        {run.isError && <p className="text-xs text-red-400">{(run.error as Error).message}</p>}
      </section>

      <InspectSignals graphId={id} revision={current?.revision} assetId="xauusd" />

      <p className="text-xs text-content-3">
        Scores measure signal strength, not the probability of profit. Each saved revision is immutable;
        every run records the exact graph revision it used. Historical results do not predict future performance.
      </p>
    </div>
  );
}

/** Read-only signal inspector: replays the saved revision over historical
 *  candles and reports what each node emitted. Signals are not trades — the
 *  engine decides entries/exits; this panel only explains node behavior. */
function InspectSignals({ graphId, revision, assetId }: { graphId: string; revision?: number; assetId: string }) {
  const diag = useGraphDiagnostics(graphId, revision);
  const [form, setForm] = useState({ timeframe: '1d', startDate: '2020-01-01', endDate: '2021-06-30', traceLimit: 200 });
  const [sel, setSel] = useState(0);
  const d = diag.data;
  const nodeIds = d?.nodes.map((n) => n.nodeId) ?? [];
  return (
    <section className="card space-y-3" aria-label="Inspect signals">
      <h3 className="section-label">INSPECT SIGNALS — revision {revision} (historical, read-only)</h3>
      <div className="flex flex-wrap items-end gap-3 text-xs">
        <label>TF <select className="bg-surface-2 border border-line px-1" value={form.timeframe}
          onChange={(e) => setForm({ ...form, timeframe: e.target.value })}>
          {['15m', '1h', '4h', '1d'].map((t) => <option key={t}>{t}</option>)}</select></label>
        <label>Start <input type="date" className="bg-surface-2 border border-line px-1" value={form.startDate}
          onChange={(e) => setForm({ ...form, startDate: e.target.value })} /></label>
        <label>End <input type="date" className="bg-surface-2 border border-line px-1" value={form.endDate}
          onChange={(e) => setForm({ ...form, endDate: e.target.value })} /></label>
        <label>Trace limit <input type="number" className="w-20 bg-surface-2 border border-line px-1 tnum" value={form.traceLimit}
          onChange={(e) => setForm({ ...form, traceLimit: Number(e.target.value) })} /></label>
        <button className="btn" disabled={diag.isPending || !revision}
          onClick={() => { setSel(0); diag.mutate({ assetId, timeframe: form.timeframe as never,
            startDate: form.startDate, endDate: form.endDate, traceLimit: form.traceLimit }); }}>
          {diag.isPending ? 'Inspecting…' : 'Inspect signals'}
        </button>
      </div>
      {diag.isError && <p className="text-xs text-red-400">{(diag.error as Error).message}</p>}
      {d && (
        <>
          <p className="text-xs text-content-3">
            {d.evaluatedBars} bars evaluated · warm-up {d.warmupBars} (never counted) · dataset{' '}
            <span className="tnum">{d.dataset}</span> · last candle {d.lastCandle}
          </p>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead><tr className="text-content-3 text-left">
                <th className="pr-3">Node</th><th className="pr-3">Type</th><th className="pr-3 tnum">BUY</th>
                <th className="pr-3 tnum">SELL</th><th className="pr-3 tnum">NEUTRAL</th>
                <th className="pr-3 tnum">Actionable %</th><th className="pr-3 tnum">Avg score</th><th>Top reasons</th>
              </tr></thead>
              <tbody>
                {d.nodes.map((n) => (
                  <tr key={n.nodeId} className="border-t border-line">
                    <td className="pr-3 py-1">{n.nodeId}</td>
                    <td className="pr-3 text-content-3">{n.nodeType}</td>
                    <td className="pr-3 tnum">{n.buy}</td>
                    <td className="pr-3 tnum">{n.sell}</td>
                    <td className="pr-3 tnum">{n.neutral}</td>
                    <td className="pr-3 tnum">{n.actionablePct.toFixed(1)}%</td>
                    <td className="pr-3 tnum">{n.avgScore != null ? n.avgScore.toFixed(3) : '—'}</td>
                    <td className="text-content-3">{n.topReasons.map((r) => `${r.reason} (${r.count})`).join(' · ')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {d.traces.length > 0 && (
            <div>
              <p className="section-label">
                EVENT TRACE — {d.traces.length} of {d.evaluatedBars} candles{d.truncated ? ' (truncated)' : ''} · click a row to inspect node outputs
              </p>
              <div className="max-h-56 overflow-y-auto border border-line">
                <table className="w-full text-xs">
                  <thead><tr className="text-content-3 text-left">
                    <th className="pr-3">Timestamp</th>
                    {nodeIds.map((nid) => <th key={nid} className="pr-3">{nid}</th>)}
                  </tr></thead>
                  <tbody>
                    {d.traces.slice(0, 300).map((row, i) => (
                      <tr key={i} onClick={() => setSel(i)}
                        className={`border-t border-line cursor-pointer ${i === sel ? 'bg-surface-2' : ''}`}>
                        <td className="pr-3 py-0.5 tnum">{formatDateTime(row.timestamp)}</td>
                        {nodeIds.map((nid) => {
                          const e = row.nodes.find((x) => x.nodeId === nid);
                          return (
                            <td key={nid} className="pr-3">
                              {e ? <span className={e.action === 'BUY' ? 'text-buy' : e.action === 'SELL' ? 'text-sell' : 'text-content-3'}>
                                {e.action}{e.action !== 'NEUTRAL' ? ` ${e.score.toFixed(2)}` : ''}</span> : '—'}
                            </td>
                          );
                        })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {d.traces[sel] && (
                <div className="text-xs space-y-0.5 border-t border-line pt-2">
                  <p className="section-label">SELECTED — {formatDateTime(d.traces[sel].timestamp)}</p>
                  {d.traces[sel].nodes.map((e) => (
                    <p key={e.nodeId}>
                      <span className="text-content-3">{e.nodeId}:</span>{' '}
                      <span className={e.action === 'BUY' ? 'text-buy' : e.action === 'SELL' ? 'text-sell' : 'text-content-3'}>
                        {e.action} {e.score.toFixed(2)}</span>{' '}
                      <span className="text-content-3">{e.reason}</span>
                    </p>
                  ))}
                </div>
              )}
            </div>
          )}
          <p className="text-xs text-content-3">
            Signals are not trades — a backtest may open fewer positions than signals emitted.
            Node scores are signal strength, not profit probability.
          </p>
        </>
      )}
    </section>
  );
}
