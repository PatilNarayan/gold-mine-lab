import { describe, expect, it } from 'vitest';
import { backtestFormBaseSchema, buildParametersSchema, validateBacktestForm } from './backtestForm';
import { MOCK_STRATEGIES } from '@/mock/strategies';

const base = {
  assetId: 'xauusd',
  strategyId: 'ema-cross',
  timeframe: '1h' as const,
  startDate: '2024-03-01',
  endDate: '2024-11-30',
  initialCapital: 100_000,
  positionSize: 5,
  commission: 0.02,
  slippage: 0.05,
};

describe('backtest form validation', () => {
  it('accepts a valid base form', () => {
    expect(backtestFormBaseSchema.safeParse(base).success).toBe(true);
  });

  it('rejects end date before start date', () => {
    const r = backtestFormBaseSchema.safeParse({ ...base, endDate: '2024-01-01' });
    expect(r.success).toBe(false);
  });

  it('rejects non-positive numbers and out-of-range values', () => {
    expect(backtestFormBaseSchema.safeParse({ ...base, initialCapital: 0 }).success).toBe(false);
    expect(backtestFormBaseSchema.safeParse({ ...base, positionSize: 150 }).success).toBe(false);
    expect(backtestFormBaseSchema.safeParse({ ...base, commission: -1 }).success).toBe(false);
  });

  it('builds a parameter schema from the strategy spec', () => {
    const strategy = MOCK_STRATEGIES.find((s) => s.id === 'ema-cross')!;
    const schema = buildParametersSchema(strategy.parameters);
    expect(
      schema.safeParse({ fastLength: 20, slowLength: 50, stopLossPct: 1.5, direction: 'both' }).success,
    ).toBe(true);
    // out-of-range param rejected
    expect(
      schema.safeParse({ fastLength: 500, slowLength: 50, stopLossPct: 1.5, direction: 'both' })
        .success,
    ).toBe(false);
    // bad enum value rejected
    expect(
      schema.safeParse({ fastLength: 20, slowLength: 50, stopLossPct: 1.5, direction: 'nope' })
        .success,
    ).toBe(false);
  });

  it('returns field-keyed errors on failure', () => {
    const strategy = MOCK_STRATEGIES.find((s) => s.id === 'ema-cross')!;
    const result = validateBacktestForm(
      { ...base, endDate: '2024-01-01', parameters: { fastLength: 1, slowLength: 50, stopLossPct: 1.5, direction: 'both' } },
      strategy.parameters,
    );
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.errors['endDate']).toBeTruthy();
      expect(result.errors['parameters.fastLength']).toBeTruthy();
    }
  });
});
