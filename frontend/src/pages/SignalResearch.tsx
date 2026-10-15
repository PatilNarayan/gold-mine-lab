import { useState } from 'react';
import { useNavigate, useParams, Link } from 'react-router-dom';
import { DemoBanner } from '@/components/common/DemoBanner';
import { SkeletonRows } from '@/components/common/Skeleton';
import { useSignalResearchList, useSignalResearch, useRunSignalResearch } from '@/hooks/useSignalResearch';
import { useStrategies } from '@/hooks/useStrategies';
import type { SignalResearch as SR, Timeframe } from '@/types/domain';
import { formatDateTime } from '@/lib/formatters';

const MAX_LEGS = 8;

/**
 * Cross-strategy signal research: replay 2+ strategies / saved graph
 * revisions over ONE shared candle window and compare where their SIGNALS
 * agree, conflict, or stay neutral — plus an optional forward price study.
 * Signals are not trades; forward returns are gross price moves, not P&L.
 */
export default function SignalResearch() {
  const { id } = useParams<{ id?: string }>();
  return id ? <ResearchDetail id={id} /> : <ResearchWorkspace />;
}

function ResearchWorkspace() {
  const navigate = useNavigate();
  const strategies = useStrategies();
  const analyses = useSignalResearchList();
  const run = useRunSignalResearch();
  const [selected, setSelected] = useState<string[]>([]);
  const [timeframe, setTimeframe] = useState<Timeframe>('1d');
  const [startDate, setStartDate] = useState('2020-01-01');
  const [endDate, setEndDate] = useState('2021-06-30');
  const [eventStudy, setEventStudy] = useState(true);
  const nameOf = (sid: string) =>
    (strategies.data ?? []).find((s) => s.id === sid)?.name ?? sid;

  const toggle = (sid: string) =>
    setSelected((cur) =>
      cur.includes(sid) ? cur.filter((x) => x !== sid) : cur.length < MAX_LEGS ? [...cur, sid] : cur,
    );

  const submit = () => {
    run.mutate(
      {
        legs: selected.map((sid) => ({ strategyId: sid })),
        assetId: 'xauusd', timeframe, startDate, endDate, eventStudy,
      },
      { onSuccess: (r) => navigate(`/research/${r.id}`) },
    );
  };

  return (
    <div className="space-y-4">
      <DemoBanner />
      <h1 className="text-lg font-semibold text-content-1">Signal research</h1>
      <p className="text-xs text-content-3">
        Replay multiple strategies over the same historical candles and compare where their
        signals agree or conflict. Signals are not trades — no backtest runs are created.
      </p>

      <section className="card space-y-3" aria-label="Analysis builder">
        <h3 className="section-label">CANDIDATES — select 2–{MAX_LEGS} (strategies + saved graph revisions)</h3>
        <div className="flex flex-wrap gap-2">
          {(strategies.data ?? []).map((s) => (
            <button key={s.id} onClick={() => toggle(s.id)}
              className={`px-2 py-1 text-xs border ${selected.includes(s.id) ? 'border-accent text-content-1 bg-surface-2' : 'border-line text-content-3'}`}>
              {s.name}{s.id.startsWith('graph:') ? ' (graph)' : ''} v{s.version}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap items-end gap-3 text-xs">
          <label>TF <select className="bg-surface-2 border border-line px-1" value={timeframe}
            onChange={(e) => setTimeframe(e.target.value as Timeframe)}>
            {['15m', '1h', '4h', '1d'].map((t) => <option key={t}>{t}</option>)}</select></label>
          <label>Start <input type="date" className="bg-surface-2 border border-line px-1" value={startDate}
            onChange={(e) => setStartDate(e.target.value)} /></label>
          <label>End <input type="date" className="bg-surface-2 border border-line px-1" value={endDate}
            onChange={(e) => setEndDate(e.target.value)} /></label>
          <label className="flex items-center gap-1">
            <input type="checkbox" checked={eventStudy} onChange={(e) => setEventStudy(e.target.checked)} />
            Forward price study (BUY episodes, horizons 1/5/10/20)
          </label>
          <button className="btn" disabled={run.isPending || selected.length < 2} onClick={submit}>
            {run.isPending ? 'Analyzing…' : `Analyze ${selected.length} candidates`}
          </button>
        </div>
        {run.isError && <p className="text-xs text-red-400">{(run.error as Error).message}</p>}
      </section>

      <section className="card space-y-2" aria-label="Analysis history">
        <h3 className="section-label">SAVED ANALYSES</h3>
        {analyses.isLoading ? <SkeletonRows rows={4} /> : (analyses.data ?? []).length === 0 ? (
          <p className="text-xs text-content-3">No analyses yet.</p>
        ) : (
          <table className="w-full text-xs">
            <thead><tr className="text-content-3 text-left">
              <th className="pr-3">Candidates</th><th className="pr-3">Window</th><th className="pr-3">TF</th>
              <th className="pr-3">Status</th><th className="pr-3">Created</th>
            </tr></thead>
            <tbody>
              {(analyses.data ?? []).map((r) => (
                <tr key={r.id} className="border-t border-line hover:bg-surface-2 cursor-pointer"
                  onClick={() => navigate(`/research/${r.id}`)}>
                  <td className="pr-3 py-1">{r.strategyIds.map(nameOf).join(' vs ')}</td>
                  <td className="pr-3 tnum">{r.startDate} → {r.endDate}</td>
                  <td className="pr-3">{r.timeframe}</td>
                  <td className={`pr-3 ${r.status === 'completed' ? 'text-buy' : 'text-sell'}`}>{r.status}</td>
                  <td className="pr-3 text-content-3">{formatDateTime(r.createdAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}

function ResearchDetail({ id }: { id: string }) {
  const q = useSignalResearch(id);
  const strategies = useStrategies();
  const nameOf = (sid: string) =>
    (strategies.data ?? []).find((s) => s.id === sid)?.name ?? sid;
  if (q.isLoading) return <SkeletonRows rows={8} />;
  if (q.isError || !q.data) return <p className="text-xs text-red-400">{(q.error as Error)?.message ?? 'Not found'} · <Link className="underline" to="/research">back</Link></p>;
  const r: SR = q.data;
  const smallSample = (r.candidates ?? []).some((c) => c.buyEpisodes + c.sellEpisodes < 10);
  return (
    <div className="space-y-4">
      <DemoBanner />
      <p className="text-xs"><Link className="text-content-3 underline" to="/research">← All analyses</Link></p>
      <h1 className="text-lg font-semibold text-content-1">
        {r.strategyIds.map(nameOf).join(' vs ')}
      </h1>
      <p className="text-xs text-content-3">
        {r.startDate} → {r.endDate} · {r.timeframe} · {r.evaluatedBars} bars evaluated ·
        warm-up {r.warmupBars} (never counted) · dataset <span className="tnum">{r.dataset}</span>
        {r.lastCandle ? ` · last candle ${r.lastCandle}` : ''} · status {r.status}
      </p>
      {smallSample && (
        <p className="text-xs text-yellow-400">
          Warning: fewer than 10 signal episodes for at least one candidate — treat statistics as
          exploratory, not conclusive.
        </p>
      )}

      <section className="card space-y-2" aria-label="Candidate statistics">
        <h3 className="section-label">CANDIDATE STATISTICS — episodes are consecutive same-direction runs, NOT trades</h3>
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead><tr className="text-content-3 text-left">
              <th className="pr-3">Candidate</th><th className="pr-3">Version</th>
              <th className="pr-3 tnum">BUY</th><th className="pr-3 tnum">SELL</th><th className="pr-3 tnum">NEUTRAL</th>
              <th className="pr-3 tnum">Actionable %</th><th className="pr-3 tnum">BUY eps</th>
              <th className="pr-3 tnum">SELL eps</th><th className="pr-3 tnum">Avg score</th>
            </tr></thead>
            <tbody>
              {(r.candidates ?? []).map((c) => (
                <tr key={c.strategyId} className="border-t border-line">
                  <td className="pr-3 py-1">{nameOf(c.strategyId)}</td>
                  <td className="pr-3 text-content-3">{c.version}</td>
                  <td className="pr-3 tnum">{c.buy}</td>
                  <td className="pr-3 tnum">{c.sell}</td>
                  <td className="pr-3 tnum">{c.neutral}</td>
                  <td className="pr-3 tnum">{c.actionablePct.toFixed(1)}%</td>
                  <td className="pr-3 tnum">{c.buyEpisodes}</td>
                  <td className="pr-3 tnum">{c.sellEpisodes}</td>
                  <td className="pr-3 tnum">{c.avgScore != null ? c.avgScore.toFixed(3) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="card space-y-2" aria-label="Pairwise agreement">
        <h3 className="section-label">PAIRWISE AGREEMENT — percentages are over co-actionable bars (both sides actionable)</h3>
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead><tr className="text-content-3 text-left">
              <th className="pr-3">Pair</th><th className="pr-3 tnum">Both BUY</th><th className="pr-3 tnum">Both SELL</th>
              <th className="pr-3 tnum">Opposite</th><th className="pr-3 tnum">One actionable</th>
              <th className="pr-3 tnum">Both neutral</th><th className="pr-3 tnum">Co-actionable</th>
              <th className="pr-3 tnum">Agree %</th><th className="pr-3 tnum">Conflict %</th>
            </tr></thead>
            <tbody>
              {(r.pairs ?? []).map((p) => (
                <tr key={`${p.a}/${p.b}`} className="border-t border-line">
                  <td className="pr-3 py-1">{nameOf(p.a)} / {nameOf(p.b)}</td>
                  <td className="pr-3 tnum">{p.bothBuy}</td>
                  <td className="pr-3 tnum">{p.bothSell}</td>
                  <td className="pr-3 tnum">{p.opposite}</td>
                  <td className="pr-3 tnum">{p.oneActionable}</td>
                  <td className="pr-3 tnum">{p.bothNeutral}</td>
                  <td className="pr-3 tnum">{p.coActionable}</td>
                  <td className="pr-3 tnum">{p.agreementPct != null ? `${p.agreementPct.toFixed(0)}%` : '—'}</td>
                  <td className="pr-3 tnum">{p.conflictPct != null ? `${p.conflictPct.toFixed(0)}%` : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {r.eventStudy && (r.forward ?? []).length > 0 && (
        <section className="card space-y-2" aria-label="Forward price study">
          <h3 className="section-label">
            FORWARD PRICE STUDY — gross price move after each BUY episode's first signal
            (next-open entry, h-bar-later close). NOT P&L: no costs, sizing, or management.
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead><tr className="text-content-3 text-left">
                <th className="pr-3">Candidate</th><th className="pr-3 tnum">Horizon</th>
                <th className="pr-3 tnum">Eligible</th><th className="pr-3 tnum">Excluded</th>
                <th className="pr-3 tnum">Mean %</th><th className="pr-3 tnum">Median %</th>
                <th className="pr-3 tnum">Positive %</th><th className="pr-3 tnum">Worst %</th>
              </tr></thead>
              <tbody>
                {(r.forward ?? []).flatMap((f) =>
                  f.horizons.map((h) => (
                    <tr key={`${f.strategyId}-${h.horizon}`} className="border-t border-line">
                      <td className="pr-3 py-1">{nameOf(f.strategyId)}</td>
                      <td className="pr-3 tnum">+{h.horizon}</td>
                      <td className="pr-3 tnum">{h.eligible}</td>
                      <td className="pr-3 tnum">{h.excluded}</td>
                      <td className="pr-3 tnum">{h.meanReturn != null ? h.meanReturn.toFixed(2) : '—'}</td>
                      <td className="pr-3 tnum">{h.medianReturn != null ? h.medianReturn.toFixed(2) : '—'}</td>
                      <td className="pr-3 tnum">{h.positivePct != null ? `${h.positivePct.toFixed(0)}%` : '—'}</td>
                      <td className="pr-3 tnum">{h.worstReturn != null ? h.worstReturn.toFixed(2) : '—'}</td>
                    </tr>
                  )),
                )}
              </tbody>
            </table>
          </div>
          <p className="text-xs text-content-3">
            Overlapping forward horizons share candles and are NOT independent samples. SELL signals
            are long-exit signals, not short entries — no forward SELL study is computed. Gross
            historical price movements only; nothing here predicts future performance.
          </p>
        </section>
      )}

      <p className="text-xs text-content-3">
        Scores are per-strategy signal-strength heuristics and are not comparable across strategy
        families. Historical analysis only — not live signals, trades, or predictions.
      </p>
    </div>
  );
}
