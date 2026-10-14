import type { ReactNode } from 'react';

/** Small stat tile: label + primary value + optional secondary line. */
export function StatCard({
  label,
  value,
  sub,
  icon,
  tone = 'default',
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  icon?: ReactNode;
  tone?: 'default' | 'buy' | 'sell' | 'accent';
}) {
  const valueTone =
    tone === 'buy' ? 'text-buy' : tone === 'sell' ? 'text-sell' : tone === 'accent' ? 'text-accent-fg' : 'text-content-1';
  return (
    <div className="border border-line bg-surface-1 px-3 py-2.5">
      <div className="flex items-center justify-between gap-2">
        <span className="section-label">{label}</span>
        {icon ? <span className="text-content-3">{icon}</span> : null}
      </div>
      <div className={`tnum mt-1 text-lg font-semibold ${valueTone}`}>{value}</div>
      {sub ? <div className="mt-0.5 text-2xs text-content-3">{sub}</div> : null}
    </div>
  );
}
