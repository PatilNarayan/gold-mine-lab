import { useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { Play, AlertTriangle } from 'lucide-react';
import { useComparison, useComparisons, useRunComparison, useStrategies } from '@/hooks';
import type {
  Comparison,
  ComparisonLeg,
  ComparisonRequest,
  ComparisonResult,
  Strategy,
  Timeframe,
} from '@/types/domain';
import { formatCurrency, formatDateTime, formatPercent, formatSignedCurrency } from '@/lib/formatters';
import { ApiError } from '@/types/api';
import { Badge } from '@/components/common/Badge';
import { Button } from '@/components/common/Button';
import { DemoBanner } from '@/components/common/DemoBanner';
import { EmptyState } from '@/components/common/EmptyState';
import { ErrorState } from '@/components/common/ErrorState';
import { Field } from '@/components/common/Field';
import { Input, Select } from '@/components/common/Input';
import { SkeletonRows } from '@/components/common/Skeleton';
import { DataTable, type Column } from '@/components/tables/DataTable';
import { ComparisonChart } from '@/components/charts/ComparisonChart';

/**
 * Research compare page: run 2+ strategies under IDENTICAL assumptions
 * (window, capital, sizing, costs, dataset) and inspect the results side by
 * side — optionally replayed on a strictly later out-of-sample window.
 * Independent legs are never combined into a fake portfolio curve.
 */
export default function Compare() {
  const { id } = useParams<{ id?: string }>();
  return id ? <ComparisonDetail id={id} /> : <ComparisonWorkspace />;
}

/* ---------------------------------------------------------------- builder */

function ComparisonWorkspace() {
  const navigate = useNavigate();
  const strategies = useStrategies();
  const comparisons = useComparisons();
  const runCmp = useRunComparison();
  const [err, setErr] = useState<string | null>(null);

  const [selected, setSelected] = useState<string[]>([]);
  const [assetId] = useState('xauusd');
  const [timeframe, setTimeframe] = useState<Timeframe>('1d');
  const [startDate, setStartDate] = useState('2020-01-01');
  const [endDate, setEndDate] = useState('2024-12-31');
  const [useOos, setUseOos] = useState(false);
  const [oosStart, setOosStart] = useState('2025-01-01');
  const [oosEnd, setOosEnd] = useState('2025-09-01');
  const [capital, setCapital] = useState(100_000);
  const [posSize, setPosSize] = useState(5);
  const [commission, setCommission] = useState(0.02);
  const [slippage, setSlippage] = useState(0.05);
  const [legParams, setLegParams] = useState<Record<string, Record<string, number | string | boolean>>>({});

  const defs = (strategies.data ?? []) as (Strategy & object)[];
  const chosen = defs.filter((s) => selected.includes(s.id));

  // Common timeframe must be supported by EVERY selected strategy.
  const commonTfs = useMemo(() => {
    if (chosen.length === 0) return [];
    return chosen
      .map((s) => s.supportedTimeframes ?? [s.timeframe])
      .reduce((a, b) => a.filter((t) => (b as Timeframe[]).includes(t as Timeframe)));
  }, [chosen]);

  const toggle = (id: string) => {
    setErr(null);
    setSelected((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
    );
    const s = defs.find((d) => d.id === id);
    if (s && !legParams[id]) {
      const defaults: Record<string, number | string | boolean> = {};
      for (const p of s.parameters) defaults[p.name] = p.default;
      setLegParams((prev) => ({ ...prev, [id]: defaults }));
    }
  };

  const validate = (): string | null => {
    if (selected.length < 2) return 'Select at least two strategies to compare.';
    if (!commonTfs.includes(timeframe))
      return `Timeframe ${timeframe} is not supported by every selected strategy (${commonTfs.join(', ') || 'none'}).`;
    if (!(endDate >= startDate)) return 'End date must be on or after the start date.';
    if (useOos) {
      if (!(oosStart > endDate))
        return 'Out-of-sample window must start strictly after the in-sample end date.';
      if (!(oosEnd >= oosStart)) return 'OOS end must be on or after OOS start.';
    }
    return null;
  };

  const submit = () => {
    const v = validate();
    if (v) {
      setErr(v);
      return;
    }
    const legs: ComparisonLeg[] = chosen.map((s) => ({
      strategyId: s.id,
      parameters: legParams[s.id] ?? {},
    }));
    const req: ComparisonRequest = {
      legs,
      assetId,
      timeframe,
      startDate,
      endDate,
      oosStartDate: useOos ? oosStart : null,
      oosEndDate: useOos ? oosEnd : null,
      initialCapital: capital,
      positionSize: posSize,
      commission,
      slippage,
    };
    setErr(null);
    runCmp.mutate(req, {
      onSuccess: (c) => navigate(`/compare/${c.id}`),
      onError: (e) => setErr(e instanceof ApiError ? e.message : 'Comparison failed'),
    });
  };

  return (
    <div className="space-y-4">
      <DemoBanner />
      <section className="card space-y-3" aria-label="Comparison builder">
        <h2 className="section-label">STRATEGY COMPARISON</h2>
        <p className="text-xs text-content-3">
          Every leg shares the window, capital, sizing, costs and dataset —
          identical experiment conditions. Historical results do not predict
          future performance.
        </p>

        <Field label="Strategies" htmlFor="cmp-strategies" hint="Select 2+">
          <div className="flex flex-wrap gap-2" id="cmp-strategies">
            {defs.map((s) => {
              const on = selected.includes(s.id);
              return (
                <label
                  key={s.id}
                  className={`flex cursor-pointer items-center gap-1.5 rounded-sm border px-2.5 py-1.5 text-sm transition-colors ${
                    on ? 'border-accent bg-accent-soft text-accent-fg' : 'border-line text-content-2 hover:bg-surface-2'
                  }`}
                >
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5 accent-[#c9a227]"
                    checked={on}
                    onChange={() => toggle(s.id)}
                  />
                  {s.name}
                </label>
              );
            })}
          </div>
        </Field>

        <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
          <Field label="Timeframe (common)" htmlFor="cmp-tf">
            <Select id="cmp-tf" value={timeframe} onChange={(e) => setTimeframe(e.target.value as Timeframe)}>
              {(commonTfs.length ? commonTfs : ['15m', '1h', '4h', '1d']).map((tf) => (
                <option key={tf} value={tf}>{tf}</option>
              ))}
            </Select>
          </Field>
          <Field label="In-sample start" htmlFor="cmp-is-start">
            <Input id="cmp-is-start" type="date" value={startDate} onChange={(e) => setStartDate(e.target.value)} className="tnum" />
          </Field>
          <Field label="In-sample end" htmlFor="cmp-is-end">
            <Input id="cmp-is-end" type="date" value={endDate} onChange={(e) => setEndDate(e.target.value)} className="tnum" />
          </Field>
          <Field label="Capital / Size / Cost" htmlFor="cmp-cap" hint={`$${capital} · ${posSize}% · ${commission}%/${slippage}%`}>
            <div className="flex gap-1">
              <Input id="cmp-cap" type="number" value={capital} onChange={(e) => setCapital(Number(e.target.value))} className="tnum" aria-label="Initial capital" />
            </div>
          </Field>
          <Field label="Position size %" htmlFor="cmp-size">
            <Input id="cmp-size" type="number" min={0.1} max={100} step={0.5} value={posSize} onChange={(e) => setPosSize(Number(e.target.value))} className="tnum" />
          </Field>
          <Field label="Commission %" htmlFor="cmp-comm">
            <Input id="cmp-comm" type="number" min={0} max={5} step={0.01} value={commission} onChange={(e) => setCommission(Number(e.target.value))} className="tnum" />
          </Field>
          <Field label="Slippage %" htmlFor="cmp-slip">
            <Input id="cmp-slip" type="number" min={0} max={5} step={0.01} value={slippage} onChange={(e) => setSlippage(Number(e.target.value))} className="tnum" />
          </Field>
          <Field label="Out-of-sample" htmlFor="cmp-oos">
            <label className="flex h-[34px] items-center gap-2 text-sm text-content-1">
              <input id="cmp-oos" type="checkbox" className="h-4 w-4 accent-[#c9a227]" checked={useOos} onChange={(e) => setUseOos(e.target.checked)} />
              Hold out a later window
            </label>
          </Field>
        </div>

        {useOos ? (
          <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
            <Field label="OOS start" htmlFor="cmp-oos-start" hint="must be after in-sample end">
              <Input id="cmp-oos-start" type="date" value={oosStart} onChange={(e) => setOosStart(e.target.value)} className="tnum" />
            </Field>
            <Field label="OOS end" htmlFor="cmp-oos-end">
              <Input id="cmp-oos-end" type="date" value={oosEnd} onChange={(e) => setOosEnd(e.target.value)} className="tnum" />
            </Field>
          </div>
        ) : null}

        {chosen.map((s) => (
          <fieldset key={s.id} className="border border-line p-2.5">
            <legend className="section-label px-1">{s.name} parameters</legend>
            <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
              {s.parameters.map((p) => {
                const pid = `cmp-${s.id}-${p.name}`;
                const value = legParams[s.id]?.[p.name] ?? p.default;
                const set = (v: number | string | boolean) =>
                  setLegParams((prev) => ({
                    ...prev,
                    [s.id]: { ...(prev[s.id] ?? {}), [p.name]: v },
                  }));
                if (p.type === 'boolean') {
                  return (
                    <Field key={p.name} label={p.label} htmlFor={pid}>
                      <label className="flex h-[34px] items-center gap-2 text-sm text-content-1">
                        <input id={pid} type="checkbox" className="h-4 w-4 accent-[#c9a227]" checked={Boolean(value)} onChange={(e) => set(e.target.checked)} />
                        {value ? 'Enabled' : 'Disabled'}
                      </label>
                    </Field>
                  );
                }
                if (p.type === 'select') {
                  return (
                    <Field key={p.name} label={p.label} htmlFor={pid}>
                      <Select id={pid} value={String(value)} onChange={(e) => set(e.target.value)}>
                        {(p.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
                      </Select>
                    </Field>
                  );
                }
                return (
                  <Field key={p.name} label={p.label} htmlFor={pid}>
                    <Input id={pid} type="number" min={p.min} max={p.max} step={p.step ?? 1} className="tnum" value={Number(value)} onChange={(e) => set(e.target.value === '' ? 0 : Number(e.target.value))} />
                  </Field>
                );
              })}
            </div>
          </fieldset>
        ))}

        {err ? <p className="text-xs text-sell" role="alert">{err}</p> : null}
        {runCmp.isError ? (
          <p className="text-xs text-sell" role="alert">
            {runCmp.error instanceof ApiError ? runCmp.error.message : 'Comparison failed'}
          </p>
        ) : null}
        <Button type="button" onClick={submit} disabled={runCmp.isPending || selected.length < 2}>
          <Play className="h-4 w-4" aria-hidden />
          {runCmp.isPending ? 'Running comparison…' : 'Run Comparison'}
        </Button>
      </section>

      <section className="card" aria-label="Comparison history">
        <h2 className="section-label mb-2">SAVED COMPARISONS</h2>
        {comparisons.isPending ? <SkeletonRows rows={3} /> : null}
        {comparisons.isError ? (
          <ErrorState title="Could not load comparison history." error={comparisons.error} onRetry={() => comparisons.refetch()} />
        ) : null}
        {comparisons.data && comparisons.data.length === 0 ? (
          <EmptyState title="No comparisons yet" message="Select strategies above and run your first comparison." />
        ) : null}
        {comparisons.data && comparisons.data.length > 0 ? (
          <DataTable
            ariaLabel="Comparison history"
            rows={comparisons.data}
            rowKey={(c) => c.id}
            onRowClick={(c) => navigate(`/compare/${c.id}`)}
            columns={[
              { key: 'created', header: 'Created', cell: (c) => <span className="tnum">{formatDateTime(c.createdAt)}</span>, sortValue: (c) => c.createdAt },
              { key: 'strategies', header: 'Strategies', cell: (c) => c.strategyIds.join(' vs ') },
              { key: 'window', header: 'In-sample', cell: (c) => <span className="tnum">{c.startDate} → {c.endDate}</span> },
              {
                key: 'oos', header: 'OOS',
                cell: (c) => (c.oosStartDate ? <span className="tnum">{c.oosStartDate} → {c.oosEndDate}</span> : <span className="text-content-3">—</span>),
              },
              { key: 'tf', header: 'TF', cell: (c) => <span className="tnum">{c.timeframe}</span> },
              {
                key: 'status', header: 'Status',
                cell: (c) => (
                  <Badge tone={c.status === 'completed' ? 'buy' : c.status === 'failed' ? 'sell' : 'info'}>
                    {c.status === 'partial_failed' ? 'partial' : c.status}
                  </Badge>
                ),
                sortValue: (c) => c.status,
              },
            ]}
          />
        ) : null}
      </section>
    </div>
  );
}

/* ---------------------------------------------------------------- detail  */

function ComparisonDetail({ id }: { id: string }) {
  const navigate = useNavigate();
  const cmp = useComparison(id);
  const [reload, setReload] = useState(0);
  const strategies = useStrategies();

  if (cmp.isPending) {
    return <div className="card"><SkeletonRows rows={6} /></div>;
  }
  if (cmp.isError || !cmp.data) {
    return (
      <ErrorState
        title={
          cmp.error instanceof ApiError && cmp.error.code === 'not_found'
            ? `Comparison ${id} not found.`
            : 'Could not load this comparison.'
        }
        error={cmp.error}
        onRetry={() => { setReload((n) => n + 1); void cmp.refetch(); }}
      />
    );
  }
  void reload;
  const c = cmp.data as Comparison;
  const nameOf = (sid: string) =>
    (strategies.data ?? []).find((s) => s.id === sid)?.name ?? sid;

  const results = (c.results ?? []).slice().sort((a, b) =>
    `${a.sample}:${a.run.strategyId}`.localeCompare(`${b.sample}:${b.run.strategyId}`),
  );
  const smallSample = results.filter((r) => (r.metrics?.totalTrades ?? 0) > 0 && (r.metrics?.totalTrades ?? 0) < 30);

  const metricColumns: Column<ComparisonResult>[] = [
    {
      key: 'strategy', header: 'Strategy',
      cell: (r) => (
        <div>
          <div className="text-content-1">{nameOf(r.run.strategyId)}</div>
          <div className="text-2xs text-content-3 tnum">{r.run.id.slice(0, 13)}</div>
        </div>
      ),
      sortValue: (r) => r.run.strategyId,
    },
    {
      key: 'sample', header: 'Sample',
      cell: (r) => (
        <Badge tone={r.sample === 'out_of_sample' ? 'warning' : 'info'}>
          {r.sample === 'out_of_sample' ? 'out-of-sample' : 'in-sample'}
        </Badge>
      ),
    },
    {
      key: 'window', header: 'Window',
      cell: (r) => <span className="tnum">{r.run.startDate} → {r.run.endDate}</span>,
    },
    {
      key: 'status', header: 'Status',
      cell: (r) =>
        r.run.status === 'completed' ? (
          <Badge tone="buy">completed</Badge>
        ) : (
          <Badge tone="sell">{r.run.status}{r.run.error ? `: ${r.run.error}` : ''}</Badge>
        ),
    },
    {
      key: 'netPnl', header: 'Net P&L', align: 'right',
      cell: (r) => <span className={`tnum ${pnlTone(r.metrics?.netPnl)}`}>{fmt(r.metrics?.netPnl)}</span>,
      sortValue: (r) => r.metrics?.netPnl ?? 0,
    },
    {
      key: 'ret', header: 'Return', align: 'right',
      cell: (r) => <span className={`tnum ${pnlTone(r.metrics?.returnPct)}`}>{pct(r.metrics?.returnPct)}</span>,
      sortValue: (r) => r.metrics?.returnPct ?? 0,
    },
    {
      key: 'trades', header: 'Trades', align: 'right',
      cell: (r) => <span className="tnum">{r.metrics?.totalTrades ?? '—'}</span>,
      sortValue: (r) => r.metrics?.totalTrades ?? 0,
    },
    {
      key: 'winRate', header: 'Win rate', align: 'right',
      cell: (r) => <span className="tnum">{r.metrics ? formatPercent(r.metrics.winRate) : '—'}</span>,
      sortValue: (r) => r.metrics?.winRate ?? 0,
    },
    {
      key: 'pf', header: 'Profit factor', align: 'right',
      cell: (r) => <span className="tnum">{r.metrics ? r.metrics.profitFactor.toFixed(2) : '—'}</span>,
      sortValue: (r) => r.metrics?.profitFactor ?? 0,
    },
    {
      key: 'dd', header: 'Max DD', align: 'right',
      cell: (r) => <span className="tnum text-sell">{r.metrics ? `-${formatPercent(Math.abs(r.metrics.maxDrawdown), 0)}` : '—'}</span>,
      sortValue: (r) => r.metrics?.maxDrawdown ?? 0,
    },
    {
      key: 'avg', header: 'Avg trade', align: 'right',
      cell: (r) => <span className={`tnum ${pnlTone(r.metrics?.avgTradePnl)}`}>{r.metrics ? formatSignedCurrency(r.metrics.avgTradePnl) : '—'}</span>,
      sortValue: (r) => r.metrics?.avgTradePnl ?? 0,
    },
    {
      key: 'gp', header: 'Gross +/−', align: 'right',
      cell: (r) =>
        r.metrics ? (
          <span className="tnum">{formatCurrency(r.metrics.grossProfit)} / {formatCurrency(r.metrics.grossLoss)}</span>
        ) : '—',
    },
    {
      key: 'comm', header: 'Commissions', align: 'right',
      cell: (r) => (
        <span className="tnum">{r.metrics?.totalCommissions != null ? formatCurrency(r.metrics.totalCommissions) : '—'}</span>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <DemoBanner />
      <button className="text-xs text-content-3 hover:text-content-1" onClick={() => navigate('/compare')}>
        ← All comparisons
      </button>

      <section className="card space-y-2" aria-label="Comparison header">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-sm font-semibold text-content-1">
            {c.strategyIds.map(nameOf).join(' vs ')}
          </h1>
          <Badge tone={c.status === 'completed' ? 'buy' : c.status === 'failed' ? 'sell' : 'warning'}>
            {c.status === 'partial_failed' ? 'partially failed' : c.status}
          </Badge>
          <span className="text-2xs text-content-3 tnum">{c.id}</span>
        </div>
        <p className="text-2xs text-content-3">
          {c.assetId.toUpperCase()} · {c.timeframe} · in-sample {c.startDate} → {c.endDate}
          {c.oosStartDate ? ` · OOS ${c.oosStartDate} → ${c.oosEndDate}` : ''}
          {' '}· initial {formatCurrency(c.initialCapital)} · size {c.positionSize}% ·
          commission {c.commission}% · slippage {c.slippage}% · {formatDateTime(c.createdAt)}
        </p>
        {c.error ? (
          <p className="flex items-center gap-1 text-xs text-warn" role="alert">
            <AlertTriangle className="h-3.5 w-3.5" aria-hidden /> {c.error}
          </p>
        ) : null}
      </section>

      {smallSample.length > 0 ? (
        <p className="flex items-center gap-1.5 rounded-sm border border-line bg-surface-1 px-3 py-2 text-xs text-warn" role="alert">
          <AlertTriangle className="h-4 w-4 shrink-0" aria-hidden />
          {smallSample.length} run(s) have fewer than 30 trades — too few for
          meaningful comparison. Treat the figures as exploratory only.
        </p>
      ) : null}

      <section className="card" aria-label="Results">
        <h2 className="section-label mb-2">RESULTS — per-run, identical assumptions</h2>
        <DataTable
          ariaLabel="Comparison results"
          rows={results}
          rowKey={(r) => r.run.id}
          onRowClick={(r) => navigate(`/results/${r.run.id}`)}
          columns={metricColumns}
        />
        <p className="mt-2 text-2xs text-content-3">
          Net P&amp;L and returns are after commission and adverse slippage on
          each leg&apos;s own account — legs are independent experiments, not
          one portfolio. Profit factor = gross profit ÷ gross loss (0 when no
          losing trades). Past performance does not predict future results.
        </p>
      </section>

      <section className="grid gap-4 lg:grid-cols-2">
        <div className="card" aria-label="Equity curves">
          <h2 className="section-label mb-2">EQUITY — per run</h2>
          <ComparisonChart results={results} nameOf={nameOf} field="equity" height={300} />
        </div>
        <div className="card" aria-label="Drawdown curves">
          <h2 className="section-label mb-2">DRAWDOWN — per run</h2>
          <ComparisonChart results={results} nameOf={nameOf} field="drawdown" height={300} />
        </div>
      </section>

      <section className="card" aria-label="Saved configuration">
        <h2 className="section-label mb-2">SAVED CONFIGURATION</h2>
        <div className="grid gap-2 sm:grid-cols-2">
          {c.strategyIds.map((sid) => {
            const legRun = results.find((r) => r.run.strategyId === sid && r.sample === 'in_sample')?.run
              ?? results.find((r) => r.run.strategyId === sid)?.run;
            return (
              <div key={sid} className="rounded-sm border border-line p-2.5 text-xs">
                <div className="text-content-1">{nameOf(sid)}</div>
                <dl className="mt-1 space-y-0.5 text-content-3 tnum">
                  {legRun ? Object.entries(legRun.parameters).map(([k, v]) => (
                    <div key={k} className="flex justify-between"><dt>{k}</dt><dd>{String(v)}</dd></div>
                  )) : <div>parameters not recorded</div>}
                  {legRun?.dataset ? <div className="flex justify-between"><dt>dataset</dt><dd>{legRun.dataset}</dd></div> : null}
                  {legRun?.engineVersion ? <div className="flex justify-between"><dt>engine</dt><dd>{legRun.engineVersion}</dd></div> : null}
                </dl>
              </div>
            );
          })}
        </div>
      </section>
    </div>
  );
}

function pnlTone(v?: number) {
  if (v == null) return 'text-content-2';
  return v >= 0 ? 'text-buy' : 'text-sell';
}
function fmt(v?: number) {
  return v == null ? '—' : formatSignedCurrency(v);
}
function pct(v?: number) {
  return v == null ? '—' : formatPercent(v);
}
