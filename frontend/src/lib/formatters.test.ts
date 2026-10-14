import { describe, expect, it } from 'vitest';
import {
  formatCurrency,
  formatDate,
  formatDateTime,
  formatPercent,
  formatPrice,
  formatSignedCurrency,
  formatSignedPercent,
} from './formatters';

describe('formatters', () => {
  it('formats currency with two decimals', () => {
    expect(formatCurrency(1234.5)).toBe('$1,234.50');
    expect(formatCurrency(0)).toBe('$0.00');
  });

  it('formats signed currency', () => {
    expect(formatSignedCurrency(25.5)).toBe('+$25.50');
    expect(formatSignedCurrency(-25.5)).toBe('-$25.50');
  });

  it('formats prices with configurable precision', () => {
    expect(formatPrice(1.0852, 4)).toBe('1.0852');
    expect(formatPrice(2341.5)).toBe('2,341.50');
  });

  it('formats percent from fractions', () => {
    expect(formatPercent(0.423)).toBe('42.3%');
    expect(formatPercent(-0.083)).toBe('-8.3%');
    expect(formatSignedPercent(0.1)).toBe('+10.0%');
  });

  it('formats ISO timestamps in UTC', () => {
    expect(formatDate('2024-06-14T15:30:00.000Z')).toBe('2024-06-14');
    expect(formatDateTime('2024-06-14T15:30:00.000Z')).toContain('2024-06-14');
    expect(formatDateTime('not-a-date')).toBe('—');
  });
});
