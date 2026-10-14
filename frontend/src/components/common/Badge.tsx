import type { ReactNode } from 'react';

type Tone = 'default' | 'accent' | 'buy' | 'sell' | 'neutral' | 'warning' | 'info';

const tones: Record<Tone, string> = {
  default: 'border-line text-content-2 bg-surface-2',
  accent: 'border-accent/40 text-accent-fg bg-accent-soft',
  buy: 'border-buy/40 text-buy bg-buy/10',
  sell: 'border-sell/40 text-sell bg-sell/10',
  neutral: 'border-line-strong text-content-2 bg-surface-2',
  warning: 'border-warning/40 text-warning bg-warning/10',
  info: 'border-info/40 text-info bg-info/10',
};

export function Badge({ tone = 'default', children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-sm border px-1.5 py-0.5 text-2xs font-semibold uppercase tracking-wide ${tones[tone]}`}
    >
      {children}
    </span>
  );
}
