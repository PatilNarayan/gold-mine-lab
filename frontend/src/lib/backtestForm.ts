import { z } from 'zod';
import type { ParameterField } from '@/types/domain';
import { timeframeSchema } from '@/types/domain';

/**
 * Validation for the backtest form. The fixed part is a Zod schema; the
 * strategy parameter part is built dynamically from the selected strategy's
 * parameter schema (never hard-coded per strategy).
 */

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

const baseObjectSchema = z.object({
  assetId: z.string().min(1, 'Select an asset'),
  strategyId: z.string().min(1, 'Select a strategy'),
  timeframe: timeframeSchema,
  startDate: z.string().regex(DATE_RE, 'Use YYYY-MM-DD'),
  endDate: z.string().regex(DATE_RE, 'Use YYYY-MM-DD'),
  initialCapital: z
    .number({ invalid_type_error: 'Enter a number' })
    .positive('Must be positive')
    .max(100_000_000, 'Too large'),
  positionSize: z
    .number({ invalid_type_error: 'Enter a number' })
    .positive('Must be positive')
    .max(100, 'Must be <= 100% of equity'),
  commission: z
    .number({ invalid_type_error: 'Enter a number' })
    .min(0, 'Cannot be negative')
    .max(5, 'Must be <= 5%'),
  slippage: z
    .number({ invalid_type_error: 'Enter a number' })
    .min(0, 'Cannot be negative')
    .max(5, 'Must be <= 5%'),
});

const dateRangeCheck = (v: { startDate: string; endDate: string }) => v.endDate > v.startDate;
const dateRangeIssue = { message: 'End date must be after start date', path: ['endDate'] };

export const backtestFormBaseSchema = baseObjectSchema.refine(dateRangeCheck, dateRangeIssue);

export type BacktestFormBase = z.infer<typeof backtestFormBaseSchema>;

/** Build a Zod object schema for one strategy's parameter fields. */
export function buildParametersSchema(fields: ParameterField[]): z.ZodType<Record<string, unknown>> {
  const shape: Record<string, z.ZodTypeAny> = {};
  for (const f of fields) {
    switch (f.type) {
      case 'number': {
        let s = z.number({ invalid_type_error: `${f.label}: enter a number` });
        if (f.min !== undefined) s = s.min(f.min, `${f.label}: min ${f.min}`);
        if (f.max !== undefined) s = s.max(f.max, `${f.label}: max ${f.max}`);
        shape[f.name] = s;
        break;
      }
      case 'select':
        shape[f.name] = f.options?.length
          ? z.enum([f.options[0], ...f.options.slice(1)])
          : z.string();
        break;
      case 'boolean':
        shape[f.name] = z.boolean();
        break;
    }
  }
  return z.object(shape);
}

export interface BacktestFormValues extends BacktestFormBase {
  parameters: Record<string, number | string | boolean>;
}

/** Validate the whole form: base fields + dynamic parameters. */
export function validateBacktestForm(
  values: unknown,
  fields: ParameterField[],
): { success: true; data: BacktestFormValues } | { success: false; errors: Record<string, string> } {
  const paramsSchema = buildParametersSchema(fields);
  const full = baseObjectSchema
    .extend({ parameters: paramsSchema })
    .refine(dateRangeCheck, dateRangeIssue);
  const parsed = full.safeParse(values);
  if (parsed.success) {
    return { success: true, data: parsed.data as BacktestFormValues };
  }
  const errors: Record<string, string> = {};
  for (const issue of parsed.error.issues) {
    const key = issue.path.join('.') || 'form';
    if (!errors[key]) errors[key] = issue.message;
  }
  return { success: false, errors };
}
