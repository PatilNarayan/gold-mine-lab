import { describe, expect, it } from 'vitest';
import { mockResearchStudyService as service } from './mockResearchStudies';
import type { ResearchStudyRequest } from '@/types/domain';

function request(
  name: string,
  holdoutStart = '2021-01-01',
  holdoutEnd = '2021-12-31',
  assetId = 'mock-test-asset',
): ResearchStudyRequest {
  return {
    name,
    description: '',
    hypothesis: 'The preregistered candidates have distinct forward outcomes.',
    assetId,
    timeframe: '1d',
    devStart: '2015-01-01',
    devEnd: '2020-12-31',
    holdoutStart,
    holdoutEnd,
    spec: {
      candidates: [{ strategyId: 'ema-cross', parameters: { fast_period: 12, slow_period: 26 } }],
      primaryMetric: 'forward_event_control_diff',
      primaryHorizon: 10,
      regimeAnalysis: true,
      specVersion: 'study-spec-v1',
    },
  };
}

describe('mock research study lifecycle', () => {
  it('freezes, runs dev, consumes once, then releases its reservation', async () => {
    const first = await service.create(request(`mock-lifecycle-${Date.now()}`, '2021-01-01', '2021-12-31', 'mock-lifecycle-test'));
    const frozen = await service.freeze(first.id);
    expect(frozen.status).toBe('frozen');
    expect(frozen.researchCounts?.notice).toMatch(/no correction applied/);
    const explored = await service.runExploratory(first.id);
    expect(explored.status).toBe('exploratory_complete');
    const consumed = await service.validateHoldout(first.id);
    expect(consumed.status).toBe('holdout_consumed');
    const repeated = await service.validateHoldout(first.id);
    expect(repeated.executions).toHaveLength(consumed.executions.length);
    await expect(service.update(first.id, request('changed'))).rejects.toThrow(/Only draft/);

    const overlapping = await service.create(request(`mock-overlap-${Date.now()}`, '2021-12-31', '2022-06-01', 'mock-lifecycle-test'));
    await expect(service.freeze(overlapping.id)).resolves.toMatchObject({ status: 'frozen' });
  });

  it('rejects partial holdout overlaps while a reservation is active', async () => {
    const first = await service.create(request(`mock-reserve-${Date.now()}`, '2021-01-01', '2021-12-31', 'mock-overlap-test'));
    await service.freeze(first.id);
    const overlap = await service.create(request(`mock-conflict-${Date.now()}`, '2021-12-31', '2022-06-01', 'mock-overlap-test'));
    await expect(service.freeze(overlap.id)).rejects.toThrow(/overlaps reservation/);
  });
});
