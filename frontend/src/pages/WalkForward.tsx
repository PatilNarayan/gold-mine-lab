import { useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { DemoBanner } from '@/components/common/DemoBanner';
import { Field } from '@/components/common/Field';
import { Input, Select } from '@/components/common/Input';
import { Button } from '@/components/common/Button';
import { ComparisonChart } from '@/components/charts/ComparisonChart';
import { ApiError } from '@/types/api';
import type {
  Strategy, Timeframe, WalkForwardFold, WalkForwardRequest,
  WalkForward, ComparisonResult,
} from '@/types/domain';
import { formatCurrency, formatDateTime, formatPercent, formatSignedCurrency } from '@/lib/formatters';
import { useStrategies } from '@/hooks';
import { useWalkForward, useWalkForwards, usePreviewWalkForward, useRunWalkForward } from '@/hooks';

const MAX_STRATEGIES = 4;
const TF_ORDER: Timeframe[] = ['1m', '5m', '15m', '1h', '4h', '1d'];

/**
 * Walk-Forward Research — fixed-configuration robustness analysis. No
 * optimization: each leg keeps one parameter set across every fold; the OOS
 * summary is evidence about stability, never a winning-strategy verdict.
 */
export function WalkForwardPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const strategies = useStrategies();
  const experiments = useWalkForwards();
  const detail = useWalkForward(id);
  const runWf = useRunWalkForward();
  const preview = usePreviewWalkForward();
  const [err, setErr] = useState<string | null>(null);

  const [selected, setSelected] = useState<string[]>([]);
  const [assetId] = useState('xauusd');
  const [timeframe, setTimeframe] = useState<Timeframe>('1d');
  const [startDate, setStartDate] = useState('2008-01-01');
  const [endDate, setEndDate] = useState('2025-09-30');
  const [isMonths, setIsMonths] = useState(24);
  const [oosMonths, setOosMonths] = useState(12);
  const [stepMonths, setStepMonths] = useState(12);
  const [capital, setCapital] = useState(100_000);
  const [posSize, setPosSize] = useState(5);
  const [commission, setCommission] = useState(0.02);
  const [slippage, setSlippage] = useState(0.05);
  const [legParams, setLegParams] = useState<Record<string, Record<string, number | string | boolean>>>({});

  const defs = (strategies.data ?? []) as Strategy[];
  const chosen = defs.filter((s) => selected.includes(s.id));
  const nameOf = (sid: string) => defs.find((s) => s.id === sid)?.name ?? sid;

  const commonTfs = useMemo(() => {
    if (chosen.length === 0) return TF_ORDER;
    return chosen
      .map((s) => s.supportedTimeframes ?? [s.timeframe])
      .reduce((a, b) => a.filter((t) => (b as Timeframe[]).includes(t as Timeframe)));
  }, [chosen]);

  const toggle = (sid: string) => {
    setErr(null);
    setSelected((prev) => (prev.includes(sid) ? prev.filter((x) => x !== sid) : [...prev, sid]));
    const s = defs.find((d) => d.id === sid);
    if (s && !legParams[sid]) {
      const defaults: Record<string, number | string | boolean> = {};
      for (const p of s.parameters) defaults[p.name] = p.default;
      setLegParams((prev) => ({ ...prev, [sid]: defaults }));
    }
  };

  const buildRequest = (): WalkForwardRequest | string => {
    if (selected.length < 1) return 'Select at least one strategy.';
    if (selected.length > MAX_STRATEGIES) return `At most ${MAX_STRATEGIES} strategies per experiment.`;
    if (!(endDate > startDate)) return 'Range end must be after range start.';
    if (isMonths < 1 || oosMonths < 1 || stepMonths < 1) return 'In-sample, OOS and step lengths must be ≥ 1 month.';
    if (stepMonths < oosMonths) return 'Step must be ≥ OOS length — OOS windows may never overlap.';
    if (!commonTfs.includes(timeframe)) return `Timeframe ${timeframe} is not supported by every selected strategy.`;
    return {
      legs: chosen.map((s) => ({ strategyId: s.id, parameters: legParams[s.id] ?? {} })),
      assetId, timeframe, startDate, endDate,
      inSampleMonths: isMonths, oosMonths, stepMonths,
      initialCapital: capital, positionSize: posSize, commission, slippage,
    };
  };

  const doPreview = () => {
    const r = buildRequest();
    if (typeof r === 'string') { setErr(r); return; }
    setErr(null);
    preview.mutate(r, { onError: (e) => setErr(e instanceof ApiError ? e.message : 'Preview failed') });
  };

  const doRun = () => {
    const r = buildRequest();
    if (typeof r === 'string') { setErr(r); return; }
    setErr(null);
    runWf.mutate(r, {
      onSuccess: (w) => navigate(`/walkforward/${w.id}`),
      onError: (e) => setErr(e instanceof ApiError ? e.message : 'Experiment failed'),
    });
  };

  if (id) {
    const w = detail.data;
    return (
      <div className="space-y-4">
        <DemoBanner />
        <button className="text-xs text-content-3 hover:text-accent-fg" onClick={() => navigate('/walkforward')}>
          ← All experiments
        </button>
        {detail.isError && <p className="card text-sm text-red-400">Failed to load experiment.</p>}
        {w && <ExperimentDetail w={w} nameOf={nameOf} />}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <DemoBanner />
      <section className="card space-y-3" aria-label="Walk-forward builder">
        <h2 className="section-label">WALK-FORWARD RESEARCH</h2>
        <p className="text-xs text-content-3">
          Fixed-configuration robustness analysis: each strategy keeps its
          parameters across rolling in-sample windows and disjoint
          out-of-sample windows. Nothing is fitted on OOS results — this is
          not optimization and does not prove profitability.
        </p>

        <Field label="Strategies" htmlFor="wf-strategies" hint="1–4">
          <div className="flex flex-wrap gap-2" id="wf-strategies">
            {defs.map((s) => {
              const on = selected.includes(s.id);
              return (
                <label key={s.id} className={`flex cursor-pointer items-center gap-1.5 rounded-sm border px-2.5 py-1.5 text-sm ${on ? 'border-accent bg-accent-soft text-accent-fg' : 'border-line text-content-2 hover:bg-surface-2'}`}>
                  <input type="checkbox" className="h-3.5 w-3.5 accent-[#c9a227]" checked={on}
                    onChange={() => toggle(s.id)}
                    disabled={!on && selected.length >= MAX_STRATEGIES} />
                  {s.name}
                </label>
              );
            })}
          </div>
        </Field>

        <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
          <Field label="Range start"><Input id="wf-start" type="date" value={startDate} onChange={(e) => setStartDate(e.target.value)} className="tnum" /></Field>
          <Field label="Range end"><Input id="wf-end" type="date" value={endDate} onChange={(e) => setEndDate(e.target.value)} className="tnum" /></Field>
          <Field label="Timeframe">
            <Select id="wf-tf" value={timeframe} onChange={(e) => setTimeframe(e.target.value as Timeframe)}>
              {TF_ORDER.filter((t) => commonTfs.includes(t)).map((t) => <option key={t} value={t}>{t}</option>)}
            </Select>
          </Field>
          <Field label="In-sample (months)"><Input id="wf-is" type="number" min={1} value={isMonths} onChange={(e) => setIsMonths(Number(e.target.value))} className="tnum" /></Field>
          <Field label="OOS (months)"><Input id="wf-oos" type="number" min={1} value={oosMonths} onChange={(e) => setOosMonths(Number(e.target.value))} className="tnum" /></Field>
          <Field label="Step (months)" hint="≥ OOS"><Input id="wf-step" type="number" min={1} value={stepMonths} onChange={(e) => setStepMonths(Number(e.target.value))} className="tnum" /></Field>
          <Field label="Capital"><Input type="number" value={capital} onChange={(e) => setCapital(Number(e.target.value))} className="tnum" /></Field>
          <Field label="Size %"><Input type="number" value={posSize} onChange={(e) => setPosSize(Number(e.target.value))} className="tnum" /></Field>
          <Field label="Commission %"><Input type="number" step={0.01} value={commission} onChange={(e) => setCommission(Number(e.target.value))} className="tnum" /></Field>
          <Field label="Slippage %"><Input type="number" step={0.01} value={slippage} onChange={(e) => setSlippage(Number(e.target.value))} className="tnum" /></Field>
        </div>

        {chosen.map((s) => (
          <div key={s.id} className="rounded-sm border border-line p-2">
            <p className="mb-1 text-xs text-content-3">{s.name} — fixed parameters for all folds</p>
            <div className="flex flex-wrap gap-2">
              {s.parameters.map((p) => (
                <label key={p.name} className="text-xs text-content-2">
                  {p.label}
                  <Input type="number" min={p.min} max={p.max} step={p.step ?? 1} className="tnum ml-1 w-20"
                    value={Number(legParams[s.id]?.[p.name] ?? p.default)}
                    onChange={(e) => setLegParams((prev) => ({ ...prev, [s.id]: { ...prev[s.id], [p.name]: Number(e.target.value) } }))} />
                </label>
              ))}
            </div>
          </div>
        ))}

        {err && <p className="rounded-sm border border-red-500/40 bg-red-500/10 px-2 py-1.5 text-sm text-red-400">{err}</p>}
        {preview.data?.folds && (() => {
          const idxs = [...new Set(preview.data!.folds!.map((f) => f.foldIndex))].sort((a, b) => a - b);
          const first = preview.data!.folds!.find((f) => f.foldIndex === idxs[0])!;
          const last = preview.data!.folds!.find((f) => f.foldIndex === idxs[idxs.length - 1])!;
          return (
            <p className="text-xs text-content-2">
              {idxs.length} fold(s) × {selected.length} leg(s): first IS {first.isStart}→{first.isEnd}, OOS {first.oosStart}→{first.oosEnd} · last OOS {last.oosStart}→{last.oosEnd}
            </p>
          );
        })()}
        <div className="flex gap-2">
          <Button variant="secondary" onClick={doPreview} disabled={preview.isPending}>
            {preview.isPending ? 'Checking…' : 'Preview folds'}
          </Button>
          <Button onClick={doRun} disabled={runWf.isPending}>
            {runWf.isPending ? 'Running folds…' : 'Run experiment'}
          </Button>
        </div>
      </section>

      <section className="card" aria-label="Experiment history">
        <h2 className="section-label">EXPERIMENT HISTORY</h2>
        {(experiments.data ?? []).length === 0 && <p className="py-3 text-sm text-content-3">No experiments yet.</p>}
        <ul className="divide-y divide-line">
          {(experiments.data ?? []).map((w) => (
            <li key={w.id}>
              <button className="flex w-full items-center justify-between px-1 py-2 text-left hover:bg-surface-2" onClick={() => navigate(`/walkforward/${w.id}`)}>
                <span className="text-sm text-content-1">
                  {w.strategyIds?.map(nameOf).join(' vs ') ?? '—'} · {w.startDate}→{w.endDate} · IS {w.inSampleMonths}m / OOS {w.oosMonths}m / step {w.stepMonths}m
                </span>
                <StatusPill status={w.status} />
              </button>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const cls = status === 'completed' ? 'text-green-400' : status === 'partial_failed' ? 'text-amber-400' : 'text-red-400';
  return <span className={`text-xs font-semibold uppercase tracking-wide ${cls}`}>{status.replace('_', ' ')}</span>;
}

function ExperimentDetail({ w, nameOf }: { w: WalkForward; nameOf: (s: string) => string }) {
  const [openFold, setOpenFold] = useState<number | null>(0);
  const folds = w.folds ?? [];
  const foldIdxs = [...new Set(folds.map((f) => f.foldIndex))].sort((a, b) => a - b);
  const summaries = w.summaries ?? {};
  const inconclusive = Object.values(summaries).some((s) => s.inconclusive);

  const oosSeries: ComparisonResult[] = folds
    .filter((f) => f.oosResult)
    .map((f) => ({ ...f.oosResult!, run: { ...f.oosResult!.run, strategyId: `${nameOf(f.strategyId)} · fold ${f.foldIndex}` } }));

  return (
    <div className="space-y-4">
      <section className="card space-y-1">
        <div className="flex items-center justify-between">
          <h2 className="section-label">{(w.strategyIds ?? []).map(nameOf).join(' vs ')}</h2>
          <StatusPill status={w.status} />
        </div>
        <p className="text-xs text-content-3">
          {w.assetId} · {w.timeframe} · range {w.startDate} → {w.endDate} · IS {w.inSampleMonths}m / OOS {w.oosMonths}m / step {w.stepMonths}m ·
          initial {formatCurrency(w.initialCapital)} · size {w.positionSize}% · commission {w.commission}% · slippage {w.slippage}%
          {w.createdAt ? ` · ${formatDateTime(w.createdAt)}` : ''}
        </p>
        {w.error && <p className="text-sm text-red-400">{w.error}</p>}
      </section>

      {inconclusive && (
        <p className="card border-amber-500/40 bg-amber-500/10 text-sm text-amber-300">
          INCONCLUSIVE: most OOS folds have fewer than 30 trades — too little evidence to judge stability.
        </p>
      )}

      <section className="card space-y-2">
        <h3 className="section-label">OOS ROBUSTNESS (per strategy, OOS folds only)</h3>
        <table className="w-full text-sm">
          <thead><tr className="text-left text-xs text-content-3">
            {['Strategy', 'Folds', 'Positive', 'Median ret', 'Worst ret', 'Worst DD', 'OOS trades', '<30-trade folds', 'Verdict'].map((h) => <th key={h} className="py-1 pr-3">{h}</th>)}
          </tr></thead>
          <tbody>
            {Object.entries(summaries).map(([sid, s]) => (
              <tr key={sid} className="border-t border-line">
                <td className="py-1.5 pr-3">{nameOf(sid)}</td>
                <td className="tnum">{s.completedFolds}{s.failedFolds ? ` (+${s.failedFolds} failed)` : ''}</td>
                <td className="tnum">{s.positiveFolds}</td>
                <td className="tnum">{formatPercent(s.medianReturnPct)}</td>
                <td className="tnum">{formatPercent(s.worstReturnPct)}</td>
                <td className="tnum">{formatPercent(-s.worstMaxDrawdown)}</td>
                <td className="tnum">{s.totalTrades}</td>
                <td className="tnum">{s.lowTradeFolds}</td>
                <td className={s.inconclusive ? 'text-amber-400' : 'text-green-400'}>{s.inconclusive ? 'inconclusive' : 'usable evidence'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      {oosSeries.length > 0 && (
        <section className="card">
          <h3 className="section-label mb-2">OOS EQUITY — INDEPENDENT FOLD CURVES (never merged)</h3>
          <ComparisonChart results={oosSeries} nameOf={(x) => x} field="equity" />
        </section>
      )}

      <section className="card">
        <h3 className="section-label">FOLDS</h3>
        {foldIdxs.map((fi) => {
          const rows = folds.filter((f) => f.foldIndex === fi);
          const first = rows[0];
          return (
            <div key={fi} className="border-t border-line py-2">
              <button className="flex w-full items-center justify-between text-left" onClick={() => setOpenFold(openFold === fi ? null : fi)}>
                <span className="text-sm text-content-1">
                  Fold {fi}: IS {first.isStart} → {first.isEnd} · OOS {first.oosStart} → {first.oosEnd}
                </span>
                <span className="text-xs text-content-3">{openFold === fi ? '▲' : '▼'}</span>
              </button>
              {openFold === fi && (
                <table className="mt-2 w-full text-xs">
                  <thead><tr className="text-left text-content-3">
                    {['Strategy', 'Sample', 'Status', 'Net P&L', 'Trades', 'Win rate', 'PF', 'Max DD', 'Commissions'].map((h) => <th key={h} className="py-1 pr-2">{h}</th>)}
                  </tr></thead>
                  <tbody>
                    {rows.map((f) => <FoldRows key={f.strategyId} f={f} nameOf={nameOf} />)}
                  </tbody>
                </table>
              )}
            </div>
          );
        })}
      </section>

      <p className="text-xs text-content-3">
        Fixed-configuration walk-forward: parameters were constant across folds and nothing was fitted on OOS results.
        Repeatedly tuning after viewing OOS can still overfit — treat an untouched final holdout as the only clean test.
        Historical results do not predict future performance.
      </p>
    </div>
  );
}


function FoldRows({ f, nameOf }: { f: WalkForwardFold; nameOf: (s: string) => string }) {
  const legs: { res: ComparisonResult | null | undefined; sample: string; start: string; end: string }[] = [
    { res: f.isResult, sample: 'IN-SAMPLE', start: f.isStart, end: f.isEnd },
    { res: f.oosResult, sample: 'OOS', start: f.oosStart, end: f.oosEnd },
  ];
  return (
    <>
      {legs.map(({ res, sample, start, end }) => {
        const m = res?.metrics;
        return (
          <tr key={sample} className="border-t border-line/50">
            <td className="py-1 pr-2">{nameOf(f.strategyId)}</td>
            <td className="pr-2">{sample} <span className="text-content-3">{start}→{end}</span></td>
            <td className="pr-2">{res ? res.run.status.toUpperCase() : (f.error ?? 'failed')}</td>
            <td className="tnum">{m ? formatSignedCurrency(m.netPnl) : '—'}</td>
            <td className="tnum">{m?.totalTrades ?? '—'}</td>
            <td className="tnum">{m ? formatPercent(m.winRate) : '—'}</td>
            <td className="tnum">{m?.profitFactor?.toFixed(2) ?? '—'}</td>
            <td className="tnum">{m ? formatPercent(-m.maxDrawdown) : '—'}</td>
            <td className="tnum">{m?.totalCommissions != null ? formatCurrency(m.totalCommissions) : '—'}</td>
          </tr>
        );
      })}
    </>
  );
}
