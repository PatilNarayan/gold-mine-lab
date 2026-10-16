import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import {
  useCreateResearchStudy, useFreezeResearchStudy, useResearchStudies, useResearchStudy,
  useRunStudyExploratory, useUpdateResearchStudy, useValidateStudyHoldout,
} from '@/hooks/useResearchStudies';
import { useStrategies } from '@/hooks/useStrategies';
import { useGraphs, useGraphRevisions } from '@/hooks/useGraphs';
import type {
  ResearchStudyRequest, StudyCandidate, Timeframe,
} from '@/types/domain';

const timeframes: Timeframe[] = ['1d', '4h', '1h', '15m', '5m', '1m'];

function blankRequest(): ResearchStudyRequest {
  return {
    name: '',
    description: '',
    hypothesis: '',
    assetId: 'xauusd',
    timeframe: '1d',
    devStart: '2015-01-01',
    devEnd: '2020-12-31',
    holdoutStart: '2021-01-01',
    holdoutEnd: '2022-12-31',
    spec: {
      candidates: [],
      primaryMetric: 'forward_event_control_diff',
      primaryHorizon: 10,
      regimeAnalysis: true,
      specVersion: 'study-spec-v1',
    },
  };
}

export default function Studies() {
  const { id } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const studies = useResearchStudies();
  const studyQuery = useResearchStudy(id);
  const create = useCreateResearchStudy();
  const update = useUpdateResearchStudy(id ?? '');
  const freeze = useFreezeResearchStudy();
  const exploratory = useRunStudyExploratory();
  const validate = useValidateStudyHoldout();
  const [draft, setDraft] = useState<ResearchStudyRequest>(blankRequest);
  const [error, setError] = useState('');
  const [confirmedStudyId, setConfirmedStudyId] = useState<string | null>(null);
  const editing = Boolean(id && location.pathname.endsWith(id) && studyQuery.data?.status === 'draft');
  const isBuilder = location.pathname === '/studies/new' || editing;

  useEffect(() => {
    if (studyQuery.data?.status === 'draft') {
      const { name, description, hypothesis, assetId, timeframe, devStart, devEnd, holdoutStart, holdoutEnd, spec } = studyQuery.data;
      setDraft({ name, description, hypothesis, assetId, timeframe, devStart, devEnd, holdoutStart, holdoutEnd, spec });
    }
  }, [studyQuery.data]);

  const submit = async () => {
    setError('');
    try {
      const study = editing
        ? await update.mutateAsync(draft)
        : await create.mutateAsync(draft);
      navigate(`/studies/${study.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to save study');
    }
  };

  const perform = async (action: () => Promise<unknown>) => {
    setError('');
    try {
      await action();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'The study action failed');
    }
  };

  if (isBuilder) {
    return (
      <StudyBuilder
        value={draft}
        onChange={setDraft}
        onSubmit={() => void submit()}
        onCancel={() => navigate(id ? `/studies/${id}` : '/studies')}
        error={error}
        saving={create.isPending || update.isPending}
        editing={editing}
      />
    );
  }

  if (id) {
    if (studyQuery.isLoading) return <p className="text-sm text-content-3">Loading study…</p>;
    if (studyQuery.isError || !studyQuery.data) return <div role="alert" className="text-danger">Study could not be loaded.</div>;
    const study = studyQuery.data;
    const locked = study.status === 'holdout_consumed';
    return (
      <div className="mx-auto max-w-6xl space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <Link to="/studies" className="text-xs text-content-3 hover:text-content-1">← All studies</Link>
            <h1 className="mt-2 text-xl font-semibold">{study.name}</h1>
            <p className="mt-1 text-sm text-content-3">{study.hypothesis}</p>
          </div>
          <span className={`rounded-full px-3 py-1 text-xs font-semibold ${locked ? 'bg-amber-100 text-amber-800' : 'bg-surface-2 text-content-2'}`}>
            {locked ? 'Locked · holdout consumed' : study.status.replaceAll('_', ' ')}
          </span>
        </div>
        {error && <p role="alert" className="rounded border border-danger/30 bg-danger/5 p-3 text-sm text-danger">{error}</p>}
        {locked && <p className="rounded border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900">Locked: this study cannot be edited and its reserved holdout has been consumed.</p>}
        {study.status === 'draft' && (
          <div className="space-y-3">
            <StudyBuilder value={draft} onChange={setDraft} onSubmit={() => void submit()} onCancel={() => navigate('/studies')} error={error} saving={update.isPending} editing />
            <button className="rounded bg-content-1 px-3 py-2 text-sm text-white disabled:opacity-50" disabled={freeze.isPending} onClick={() => {
              if (window.confirm('Freeze this exact specification and reserve its holdout window?')) {
                void perform(() => freeze.mutateAsync(study.id));
              }
            }}>Freeze and reserve holdout</button>
          </div>
        )}
        <section className="grid gap-3 md:grid-cols-2">
          <InfoCard title="Dataset and holdout reservation">
            <p><b>Dataset:</b> <code className="break-all">{study.datasetFingerprint}</code></p>
            <p><b>Development:</b> {study.devStart} – {study.devEnd}</p>
            <p><b>Reserved holdout:</b> {study.holdoutStart} – {study.holdoutEnd}</p>
            <p><b>Spec hash:</b> <code className="break-all">{study.specHash}</code></p>
          </InfoCard>
          <InfoCard title="Research counts">
            {study.researchCounts ? <>
              <p>{study.researchCounts.totalComparisons} comparisons total across {study.researchCounts.candidates} candidates and {study.researchCounts.horizons} candidate-horizons.</p>
              <p>{study.researchCounts.graphRevisions} immutable graph revisions; {study.researchCounts.regimeCells} regime cells.</p>
              <p className="mt-2 text-xs text-content-3">{study.researchCounts.notice}</p>
            </> : <p>Counts are recorded when this study is frozen.</p>}
          </InfoCard>
        </section>
        <InfoCard title="Frozen specification">
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap text-xs">{JSON.stringify(study.spec, null, 2)}</pre>
        </InfoCard>
        <div className="flex flex-wrap gap-2">
          {study.status === 'frozen' || study.status === 'exploratory_complete' ? (
            <button className="rounded bg-content-1 px-3 py-2 text-sm text-white" disabled={exploratory.isPending} onClick={() => void perform(() => exploratory.mutateAsync(study.id))}>Run development analysis</button>
          ) : null}
          {study.status === 'exploratory_complete' && (
            <button className="rounded bg-amber-700 px-3 py-2 text-sm text-white" disabled={validate.isPending} onClick={() => setConfirmedStudyId(study.id)}>Consume reserved holdout…</button>
          )}
        </div>
        {confirmedStudyId === study.id && (
          <div role="dialog" aria-modal="true" aria-label="Confirm holdout consumption" className="rounded border border-amber-400 bg-amber-50 p-4">
            <h2 className="font-semibold text-amber-950">Irreversible confirmatory validation</h2>
            <p className="my-2 text-sm text-amber-900">This consumes the reserved window {study.holdoutStart} – {study.holdoutEnd} and permanently locks this study. Continue?</p>
            <div className="flex gap-2">
              <button className="rounded bg-amber-800 px-3 py-2 text-sm text-white" onClick={() => {
                setConfirmedStudyId(null);
                void perform(() => validate.mutateAsync(study.id));
              }}>Confirm and consume holdout</button>
              <button className="rounded border px-3 py-2 text-sm" onClick={() => setConfirmedStudyId(null)}>Cancel</button>
            </div>
          </div>
        )}
        <section className="space-y-3">
          <h2 className="font-semibold">Execution history</h2>
          {study.lastError && <p role="alert" className="text-sm text-danger">Last error: {study.lastError}</p>}
          {study.executions.map((execution) => (
            <InfoCard key={execution.id} title={`${execution.kind} · ${execution.status} · ${execution.window.startDate} – ${execution.window.endDate}`}>
              {execution.error && <p className="text-sm text-danger">{execution.error}</p>}
              {execution.signalResearchId && <p className="text-xs text-content-3">Signal research: {execution.signalResearchId}</p>}
              {execution.outcomes.length > 0 && (
                <div className="overflow-x-auto">
                  <table className="w-full text-left text-xs">
                    <thead><tr><th>Candidate</th><th>Label</th><th>Metric</th><th>Horizon</th><th>Value</th><th>CI</th><th>Events / controls</th></tr></thead>
                    <tbody>{execution.outcomes.map((outcome, index) => (
                      <tr key={`${outcome.strategyId}-${outcome.horizon}-${index}`} className="border-t border-line">
                        <td>{outcome.strategyId}</td><td>{outcome.label}</td><td>{outcome.metric}</td><td>{outcome.horizon}</td>
                        <td>{outcome.value == null ? '—' : outcome.value.toFixed(4)}</td>
                        <td>{outcome.ciStatus}{outcome.ciLow != null && outcome.ciHigh != null ? ` [${outcome.ciLow.toFixed(3)}, ${outcome.ciHigh.toFixed(3)}]` : ''}</td>
                        <td>{outcome.events} / {outcome.controls}</td>
                      </tr>
                    ))}</tbody>
                  </table>
                </div>
              )}
            </InfoCard>
          ))}
          {study.executions.length === 0 && <p className="text-sm text-content-3">No executions yet.</p>}
        </section>
        <p className="text-xs text-content-3">Pre-registration records the plan and its execution history; it does not prove profitability or remove selection bias.</p>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-6xl space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-xl font-semibold">Research studies</h1><p className="text-sm text-content-3">Record a research plan before examining its outcomes.</p></div>
        <Link to="/studies/new" className="rounded bg-content-1 px-3 py-2 text-sm text-white">New study</Link>
      </div>
      {studies.isLoading && <p>Loading studies…</p>}
      {studies.isError && <p role="alert" className="text-danger">Studies could not be loaded.</p>}
      <div className="space-y-2">
        {(studies.data ?? []).map((study) => <Link key={study.id} to={`/studies/${study.id}`} className="block rounded border border-line bg-surface-1 p-3 hover:bg-surface-2">
          <div className="flex justify-between gap-3"><strong>{study.name}</strong><span className="text-xs">{study.status.replaceAll('_', ' ')}</span></div>
          <p className="mt-1 text-sm text-content-3">{study.hypothesis}</p>
          <p className="mt-2 text-xs text-content-3">{study.assetId} · {study.timeframe} · dev {study.devStart}–{study.devEnd} · holdout {study.holdoutStart}–{study.holdoutEnd}</p>
        </Link>)}
        {studies.data?.length === 0 && <p className="rounded border border-line p-4 text-sm text-content-3">No studies recorded.</p>}
      </div>
      <p className="text-xs text-content-3">Preregistration improves traceability; it does not establish profitability or eliminate selection bias.</p>
    </div>
  );
}

function StudyBuilder({ value, onChange, onSubmit, onCancel, error, saving, editing }: {
  value: ResearchStudyRequest;
  onChange: (value: ResearchStudyRequest) => void;
  onSubmit: () => void;
  onCancel: () => void;
  error: string;
  saving: boolean;
  editing: boolean;
}) {
  const strategies = useStrategies();
  const graphs = useGraphs();
  const [graphId, setGraphId] = useState('');
  const revisions = useGraphRevisions(graphId || undefined);
  const strategyOptions = useMemo(() => (strategies.data ?? []).filter((item) => !item.id.startsWith('graph:')), [strategies.data]);
  const setCandidate = (candidate: StudyCandidate, enabled: boolean) => {
    const candidates = enabled
      ? value.spec.candidates.some((item) => item.strategyId === candidate.strategyId)
        ? value.spec.candidates
        : [...value.spec.candidates, candidate]
      : value.spec.candidates.filter((item) => item.strategyId !== candidate.strategyId);
    onChange({ ...value, spec: { ...value.spec, candidates } });
  };
  const patch = (fields: Partial<ResearchStudyRequest>) => onChange({ ...value, ...fields });

  return (
    <div className="mx-auto max-w-4xl space-y-4">
      <div><h1 className="text-xl font-semibold">{editing ? 'Edit draft study' : 'Pre-register a study'}</h1>
        <p className="mt-1 text-sm text-content-3">Freeze the candidates, windows, metric and horizons before running the reserved holdout.</p></div>
      {error && <p role="alert" className="rounded border border-danger/30 p-3 text-sm text-danger">{error}</p>}
      <InfoCard title="Study details">
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Name"><input value={value.name} onChange={(e) => patch({ name: e.target.value })} /></Field>
          <Field label="Asset ID"><input value={value.assetId} onChange={(e) => patch({ assetId: e.target.value })} /></Field>
          <Field label="Description"><input value={value.description} onChange={(e) => patch({ description: e.target.value })} /></Field>
          <Field label="Timeframe"><select value={value.timeframe} onChange={(e) => patch({ timeframe: e.target.value as Timeframe })}>{timeframes.map((tf) => <option key={tf}>{tf}</option>)}</select></Field>
          <Field label="Hypothesis"><textarea value={value.hypothesis} onChange={(e) => patch({ hypothesis: e.target.value })} rows={3} /></Field>
        </div>
      </InfoCard>
      <InfoCard title="Development and reserved holdout windows">
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Development start"><input type="date" value={value.devStart} onChange={(e) => patch({ devStart: e.target.value })} /></Field>
          <Field label="Development end"><input type="date" value={value.devEnd} onChange={(e) => patch({ devEnd: e.target.value })} /></Field>
          <Field label="Holdout start"><input type="date" value={value.holdoutStart} onChange={(e) => patch({ holdoutStart: e.target.value })} /></Field>
          <Field label="Holdout end"><input type="date" value={value.holdoutEnd} onChange={(e) => patch({ holdoutEnd: e.target.value })} /></Field>
        </div>
      </InfoCard>
      <InfoCard title="Built-in candidates and parameters">
        <div className="space-y-3">
          {strategyOptions.map((strategy) => {
            const candidate = value.spec.candidates.find((item) => item.strategyId === strategy.id);
            return <div key={strategy.id} className="rounded border border-line p-3">
              <label className="flex items-center gap-2 text-sm font-medium">
                <input type="checkbox" checked={Boolean(candidate)} onChange={(e) => setCandidate({
                  strategyId: strategy.id,
                  parameters: Object.fromEntries(strategy.parameters.map((field) => [field.name, field.default])),
                }, e.target.checked)} />
                {strategy.name} · {strategy.id} · v{strategy.version}
              </label>
              {candidate && strategy.parameters.length > 0 && <div className="mt-3 grid gap-2 sm:grid-cols-2">
                {strategy.parameters.map((field) => <Field key={field.name} label={field.label}>
                  {field.type === 'select' ? <select value={String(candidate.parameters?.[field.name] ?? field.default)} onChange={(e) => patchCandidate(value, onChange, candidate, field.name, e.target.value)}>{(field.options ?? []).map((option) => <option key={option}>{option}</option>)}</select>
                    : field.type === 'boolean' ? <input type="checkbox" checked={Boolean(candidate.parameters?.[field.name] ?? field.default)} onChange={(e) => patchCandidate(value, onChange, candidate, field.name, e.target.checked)} />
                      : <input type="number" min={field.min} max={field.max} step={field.step ?? 1} value={Number(candidate.parameters?.[field.name] ?? field.default)} onChange={(e) => patchCandidate(value, onChange, candidate, field.name, Number(e.target.value))} />}
                </Field>)}
              </div>}
            </div>;
          })}
          {strategyOptions.length === 0 && <p className="text-sm text-content-3">Loading strategy metadata…</p>}
        </div>
      </InfoCard>
      <InfoCard title="Immutable graph revision">
        <div className="flex flex-wrap gap-2">
          <select aria-label="Graph" value={graphId} onChange={(e) => setGraphId(e.target.value)}>
            <option value="">Select a saved graph</option>
            {(graphs.data ?? []).map((graph) => <option key={graph.id} value={graph.id}>{graph.name}</option>)}
          </select>
          <select aria-label="Graph revision" disabled={!graphId} value="" onChange={(e) => {
            const revision = revisions.data?.find((item) => String(item.revision) === e.target.value);
            if (revision) setCandidate({ strategyId: `graph:${revision.graphId}:v${revision.revision}`, graphRevision: revision.revision, definitionHash: revision.definitionHash }, true);
          }}>
            <option value="">Add an immutable revision</option>
            {(revisions.data ?? []).map((revision) => <option key={revision.revision} value={revision.revision}>v{revision.revision} · {revision.definitionHash.slice(0, 12)}</option>)}
          </select>
        </div>
        <ul className="mt-2 list-inside list-disc text-xs text-content-3">{value.spec.candidates.filter((candidate) => candidate.strategyId.startsWith('graph:')).map((candidate) => <li key={candidate.strategyId}>{candidate.strategyId} · {candidate.definitionHash} <button className="ml-2 underline" onClick={() => setCandidate(candidate, false)}>Remove</button></li>)}</ul>
      </InfoCard>
      <InfoCard title="Primary outcome definition">
        <div className="grid gap-3 md:grid-cols-3">
          <Field label="Primary metric"><select value={value.spec.primaryMetric} onChange={(e) => onChange({ ...value, spec: { ...value.spec, primaryMetric: e.target.value as ResearchStudyRequest['spec']['primaryMetric'] } })}>
            <option value="forward_mean_return">Forward mean return</option>
            <option value="forward_event_control_diff">Forward event-control difference</option>
          </select></Field>
          <Field label="Primary horizon"><select value={value.spec.primaryHorizon} onChange={(e) => onChange({ ...value, spec: { ...value.spec, primaryHorizon: Number(e.target.value) as 1 | 5 | 10 | 20 } })}>{[1, 5, 10, 20].map((h) => <option key={h} value={h}>{h} bars</option>)}</select></Field>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={value.spec.regimeAnalysis} onChange={(e) => onChange({ ...value, spec: { ...value.spec, regimeAnalysis: e.target.checked } })} />Include regime analysis</label>
        </div>
      </InfoCard>
      <p className="text-xs text-content-3">Freeze records a plan and reserves an application-level holdout. This does not prove profitability or remove selection bias.</p>
      <div className="flex gap-2">
        <button className="rounded bg-content-1 px-3 py-2 text-sm text-white disabled:opacity-50" disabled={saving || value.spec.candidates.length === 0} onClick={onSubmit}>{saving ? 'Saving…' : editing ? 'Save draft' : 'Create draft'}</button>
        <button className="rounded border border-line px-3 py-2 text-sm" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

function patchCandidate(value: ResearchStudyRequest, onChange: (value: ResearchStudyRequest) => void, candidate: StudyCandidate, key: string, fieldValue: number | string | boolean) {
  const candidates = value.spec.candidates.map((item) => item.strategyId === candidate.strategyId
    ? { ...item, parameters: { ...item.parameters, [key]: fieldValue } }
    : item);
  onChange({ ...value, spec: { ...value.spec, candidates } });
}

function InfoCard({ title, children }: { title: string; children: ReactNode }) {
  return <section className="space-y-2 rounded border border-line bg-surface-1 p-3 text-sm"><h2 className="font-semibold">{title}</h2>{children}</section>;
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <label className="flex flex-col gap-1 text-xs text-content-3">{label}<span className="text-content-1 [&_input]:w-full [&_input]:rounded [&_input]:border [&_input]:border-line [&_input]:bg-surface-1 [&_input]:px-2 [&_input]:py-1 [&_select]:w-full [&_select]:rounded [&_select]:border [&_select]:border-line [&_select]:bg-surface-1 [&_select]:px-2 [&_select]:py-1 [&_textarea]:w-full [&_textarea]:rounded [&_textarea]:border [&_textarea]:border-line [&_textarea]:bg-surface-1 [&_textarea]:px-2 [&_textarea]:py-1">{children}</span></label>;
}
