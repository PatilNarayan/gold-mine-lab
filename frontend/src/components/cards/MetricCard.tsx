import type { ReactNode } from 'react';

/** Larger metric tile for the Results page. */
export function MetricCard({
  label,
  value,
  tone = 'default',
  hint,
}: {
  label: string;
  value: ReactNode;
  tone?: 'default' | 'buy' | 'sell';
  hint?: string;
}) {
  const valueTone = tone === 'buy' ? 'text-buy' : tone === 'sell' ? 'text-sell' : 'text-content-1';
  return (
    <div className="border border-line bg-surface-1 px-3 py-3">
      <div className="section-label">{label}</div>
      <div className={`tnum mt-1.5 text-xl font-semibold ${valueTone}`}>{value}</div>
      {hint ? <div className="mt-0.5 text-2xs text-content-3">{hint}</div> : null}
    </div>
  );
}
