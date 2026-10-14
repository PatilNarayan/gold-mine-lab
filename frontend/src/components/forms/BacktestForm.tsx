import { useMemo, useState } from 'react';
import { Loader2, Play } from 'lucide-react';
import type { Asset, BacktestRequest, Strategy, Timeframe } from '@/types/domain';
import { TIMEFRAMES } from '@/lib/constants';
import { validateBacktestForm } from '@/lib/backtestForm';
import { useBacktestDraftStore } from '@/stores/useBacktestDraftStore';
import { useAssetStore } from '@/stores/useAssetStore';
import { Button } from '@/components/common/Button';
import { Field } from '@/components/common/Field';
import { Input, Select } from '@/components/common/Input';

type Draft = {
  assetId?: string;
  strategyId?: string;
  timeframe?: Timeframe;
  startDate?: string;
  endDate?: string;
  initialCapital?: number;
  positionSize?: number;
  commission?: number;
  slippage?: number;
  parameters?: Record<string, number | string | boolean>;
};

const DEFAULTS = {
  timeframe: '1h' as Timeframe,
  startDate: '2024-03-01',
  endDate: '2024-11-30',
  initialCapital: 100_000,
  positionSize: 5,
  commission: 0.02,
  slippage: 0.05,
};

/**
 * Backtest form. Strategy parameters are rendered from the selected
 * strategy's parameter schema — nothing is hard-coded per strategy. All
 * fields validate through Zod before `onRun` fires.
 */
export function BacktestForm({
  strategies,
  assets,
  onRun,
  isPending = false,
}: {
  strategies: Strategy[];
  assets: Asset[];
  onRun: (req: BacktestRequest) => void;
  isPending?: boolean;
}) {
  const draft = useBacktestDraftStore((s) => s.draft);
  const update = useBacktestDraftStore((s) => s.update);
  const selectedAssetId = useAssetStore((s) => s.assetId);
  const [errors, setErrors] = useState<Record<string, string>>({});

  // Defaults come from the selected-asset store and the first strategy —
  // nothing is hard-coded to a specific symbol.
  const v = {
    assetId: draft.assetId ?? selectedAssetId,
    strategyId: draft.strategyId ?? strategies[0]?.id ?? '',
    timeframe: draft.timeframe ?? DEFAULTS.timeframe,
    startDate: draft.startDate ?? DEFAULTS.startDate,
    endDate: draft.endDate ?? DEFAULTS.endDate,
    initialCapital: draft.initialCapital ?? DEFAULTS.initialCapital,
    positionSize: draft.positionSize ?? DEFAULTS.positionSize,
    commission: draft.commission ?? DEFAULTS.commission,
    slippage: draft.slippage ?? DEFAULTS.slippage,
  };
  const strategy = strategies.find((s) => s.id === v.strategyId) ?? strategies[0];

  // Parameter values = strategy defaults overlaid by draft values.
  const paramValues = useMemo(() => {
    const out: Record<string, number | string | boolean> = {};
    for (const p of strategy?.parameters ?? []) {
      out[p.name] = draft.parameters?.[p.name] ?? p.default;
    }
    return out;
  }, [strategy, draft.parameters]);

  const setParam = (name: string, value: number | string | boolean) =>
    update({ parameters: { ...paramValues, [name]: value } });

  const err = (k: string) => errors[k];

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const result = validateBacktestForm(
      {
        assetId: v.assetId,
        strategyId: v.strategyId,
        timeframe: v.timeframe,
        startDate: v.startDate,
        endDate: v.endDate,
        initialCapital: v.initialCapital,
        positionSize: v.positionSize,
        commission: v.commission,
        slippage: v.slippage,
        parameters: paramValues,
      },
      strategy?.parameters ?? [],
    );
    if (!result.success) {
      setErrors(result.errors);
      return;
    }
    setErrors({});
    onRun(result.data);
  };

  const num = (key: keyof Draft, fallback: number) => (raw: string) =>
    update({ [key]: raw === '' ? fallback : Number(raw) });

  return (
    <form onSubmit={submit} noValidate className="space-y-3" aria-label="Backtest form">
      <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
        <Field label="Asset" htmlFor="bf-asset" error={err('assetId')}>
          <Select id="bf-asset" value={v.assetId} onChange={(e) => update({ assetId: e.target.value })}>
            {assets.map((a) => (
              <option key={a.id} value={a.id}>
                {a.symbol} — {a.name}
              </option>
            ))}
          </Select>
        </Field>

        <Field label="Strategy" htmlFor="bf-strategy" error={err('strategyId')}>
          <Select
            id="bf-strategy"
            value={v.strategyId}
            onChange={(e) => update({ strategyId: e.target.value, parameters: undefined })}
          >
            {strategies.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name} v{s.version}
              </option>
            ))}
          </Select>
        </Field>

        <Field label="Timeframe" htmlFor="bf-tf" error={err('timeframe')}>
          <Select
            id="bf-tf"
            value={v.timeframe}
            onChange={(e) => update({ timeframe: e.target.value as Timeframe })}
          >
            {TIMEFRAMES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </Select>
        </Field>

        <Field label="Start date" htmlFor="bf-start" error={err('startDate')}>
          <Input
            id="bf-start"
            type="date"
            className="tnum"
            value={v.startDate}
            onChange={(e) => update({ startDate: e.target.value })}
          />
        </Field>

        <Field label="End date" htmlFor="bf-end" error={err('endDate')}>
          <Input
            id="bf-end"
            type="date"
            className="tnum"
            value={v.endDate}
            onChange={(e) => update({ endDate: e.target.value })}
          />
        </Field>

        <Field label="Initial capital ($)" htmlFor="bf-capital" error={err('initialCapital')}>
          <Input
            id="bf-capital"
            type="number"
            min={1}
            step={1000}
            className="tnum"
            value={v.initialCapital}
            onChange={(e) => num('initialCapital', 0)(e.target.value)}
          />
        </Field>

        <Field label="Position size (%)" htmlFor="bf-size" error={err('positionSize')}>
          <Input
            id="bf-size"
            type="number"
            min={0.1}
            max={100}
            step={0.5}
            className="tnum"
            value={v.positionSize}
            onChange={(e) => num('positionSize', 0)(e.target.value)}
          />
        </Field>

        <Field label="Commission (%)" htmlFor="bf-comm" error={err('commission')}>
          <Input
            id="bf-comm"
            type="number"
            min={0}
            max={5}
            step={0.01}
            className="tnum"
            value={v.commission}
            onChange={(e) => num('commission', 0)(e.target.value)}
          />
        </Field>

        <Field label="Slippage (%)" htmlFor="bf-slip" error={err('slippage')}>
          <Input
            id="bf-slip"
            type="number"
            min={0}
            max={5}
            step={0.01}
            className="tnum"
            value={v.slippage}
            onChange={(e) => num('slippage', 0)(e.target.value)}
          />
        </Field>
      </div>

      {strategy && strategy.parameters.length > 0 ? (
        <fieldset className="border border-line p-2.5">
          <legend className="section-label px-1">{strategy.name} parameters</legend>
          <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
            {strategy.parameters.map((p) => {
              const value = paramValues[p.name];
              const id = `bf-p-${p.name}`;
              const error = err(`parameters.${p.name}`);
              if (p.type === 'boolean') {
                return (
                  <Field key={p.name} label={p.label} htmlFor={id} error={error}>
                    <label className="flex h-[34px] items-center gap-2 text-sm text-content-1">
                      <input
                        id={id}
                        type="checkbox"
                        checked={Boolean(value)}
                        onChange={(e) => setParam(p.name, e.target.checked)}
                        className="h-4 w-4 accent-[#c9a227]"
                      />
                      {value ? 'Enabled' : 'Disabled'}
                    </label>
                  </Field>
                );
              }
              if (p.type === 'select') {
                return (
                  <Field key={p.name} label={p.label} htmlFor={id} error={error}>
                    <Select id={id} value={String(value)} onChange={(e) => setParam(p.name, e.target.value)}>
                      {(p.options ?? []).map((o) => (
                        <option key={o} value={o}>
                          {o}
                        </option>
                      ))}
                    </Select>
                  </Field>
                );
              }
              return (
                <Field key={p.name} label={p.label} htmlFor={id} error={error} hint={p.description}>
                  <Input
                    id={id}
                    type="number"
                    min={p.min}
                    max={p.max}
                    step={p.step ?? 1}
                    className="tnum"
                    value={Number(value)}
                    onChange={(e) => setParam(p.name, e.target.value === '' ? 0 : Number(e.target.value))}
                  />
                </Field>
              );
            })}
          </div>
        </fieldset>
      ) : null}

      <div className="flex items-center gap-3">
        <Button type="submit" disabled={isPending}>
          {isPending ? (
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
          ) : (
            <Play className="h-4 w-4" aria-hidden />
          )}
          {isPending ? 'Running backtest…' : 'Run Backtest'}
        </Button>
        {isPending ? (
          <span className="text-xs text-content-3">Simulating trades against historical data…</span>
        ) : null}
      </div>
    </form>
  );
}
