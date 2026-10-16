import type {
  ResearchStudy, ResearchStudyRequest,
  StudyExecution, StudyOutcome,
} from '@/types/domain';
import type { ResearchStudyService } from '@/services/interfaces';

const studies = new Map<string, ResearchStudy>();
let serial = 0;
const clone = <T,>(value: T): T => structuredClone(value);
const now = () => '2025-01-01T00:00:00.000Z';

function required(id: string): ResearchStudy {
  const study = studies.get(id);
  if (!study) throw new Error('Research study not found');
  return study;
}

function makeOutcomes(study: ResearchStudy): StudyOutcome[] {
  return study.spec.candidates.flatMap((candidate, ci) =>
    [1, 5, 10, 20].map((horizon) => ({
      strategyId: candidate.strategyId,
      label: horizon === study.spec.primaryHorizon ? 'primary' as const : 'secondary' as const,
      metric: study.spec.primaryMetric,
      horizon,
      value: 0.12 + ci * 0.03 + horizon * 0.01,
      ciStatus: 'inconclusive',
      ciLow: -0.2,
      ciHigh: 0.4,
      events: 18,
      controls: 240,
    })),
  );
}

function run(study: ResearchStudy, kind: 'exploratory' | 'confirmatory'): ResearchStudy {
  if (kind === 'confirmatory' && study.status === 'holdout_consumed') return clone(study);
  if (kind === 'exploratory' && !['frozen', 'exploratory_complete'].includes(study.status)) {
    throw new Error('Study must be frozen before exploratory runs');
  }
  if (kind === 'confirmatory' && study.status !== 'exploratory_complete') {
    throw new Error('Study must complete an exploratory run before holdout validation');
  }
  const window = kind === 'exploratory'
    ? { startDate: study.devStart, endDate: study.devEnd }
    : { startDate: study.holdoutStart, endDate: study.holdoutEnd };
  const execution: StudyExecution = {
    id: `mock-execution-${++serial}`,
    kind,
    status: 'completed',
    startedAt: now(),
    finishedAt: now(),
    window,
    signalResearchId: `mock-signal-${serial}`,
    outcomes: makeOutcomes(study),
  };
  const updated: ResearchStudy = {
    ...study,
    status: kind === 'exploratory' ? 'exploratory_complete' : 'holdout_consumed',
    executions: [...study.executions, execution],
    exploratoryCompletedAt: kind === 'exploratory' ? (study.exploratoryCompletedAt ?? now()) : study.exploratoryCompletedAt,
    holdoutConsumedAt: kind === 'confirmatory' ? now() : study.holdoutConsumedAt,
    updatedAt: now(),
    lastError: null,
  };
  studies.set(study.id, updated);
  return clone(updated);
}

export const mockResearchStudyService: ResearchStudyService = {
  async create(request: ResearchStudyRequest) {
    const id = `mock-study-${++serial}`;
    const study: ResearchStudy = {
      ...clone(request),
      id,
      status: 'draft',
      datasetFingerprint: `${request.assetId}:${request.timeframe}@sha256:mock`,
      specHash: `mock-spec-hash-${serial}`,
      researchCounts: null,
      executions: [],
      lastError: null,
      createdAt: now(),
      updatedAt: now(),
      frozenAt: null,
      exploratoryCompletedAt: null,
      holdoutConsumedAt: null,
    };
    studies.set(id, study);
    return clone(study);
  },
  async update(id, request) {
    const study = required(id);
    if (study.status !== 'draft') throw new Error('Only draft studies can be edited');
    const updated = {
      ...study,
      ...clone(request),
      datasetFingerprint: `${request.assetId}:${request.timeframe}@sha256:mock`,
      specHash: `mock-spec-hash-${study.id}-${serial + 1}`,
      updatedAt: now(),
    };
    studies.set(id, updated);
    return clone(updated);
  },
  async list() {
    return [...studies.values()].map(clone);
  },
  async get(id) {
    return clone(required(id));
  },
  async freeze(id) {
    const study = required(id);
    if (study.status !== 'draft') throw new Error('Only draft studies can be frozen');
    const conflict = [...studies.values()].find((other) =>
      other.id !== id &&
      other.assetId === study.assetId &&
      other.timeframe === study.timeframe &&
      ['frozen', 'exploratory_complete'].includes(other.status) &&
      other.holdoutStart <= study.holdoutEnd &&
      other.holdoutEnd >= study.holdoutStart,
    );
    if (conflict) throw new Error(`Holdout overlaps reservation by study "${conflict.name}" (${conflict.holdoutStart}..${conflict.holdoutEnd})`);
    const candidateCount = study.spec.candidates.length;
    const horizons = candidateCount * 4;
    const regimeCells = study.spec.regimeAnalysis ? horizons * 8 : 0;
    const frozen: ResearchStudy = {
      ...study,
      status: 'frozen',
      frozenAt: now(),
      updatedAt: now(),
      researchCounts: {
        candidates: candidateCount,
        parameterConfigurations: candidateCount,
        graphRevisions: study.spec.candidates.filter((c) => c.graphRevision !== undefined).length,
        horizons,
        regimeCells,
        totalComparisons: horizons + regimeCells,
        notice: 'this is how many comparisons were looked at; no correction applied',
      },
    };
    studies.set(id, frozen);
    return clone(frozen);
  },
  async runExploratory(id) {
    return run(required(id), 'exploratory');
  },
  async validateHoldout(id) {
    return run(required(id), 'confirmatory');
  },
};
