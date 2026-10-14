import { FlaskConical } from 'lucide-react';
import { isMockMode } from '@/lib/config';

/**
 * Visible "demo data" marker rendered on every data screen while
 * VITE_DATA_MODE=mock. Disappears entirely in api mode.
 */
export function DemoBanner({ compact = false }: { compact?: boolean }) {
  if (!isMockMode) return null;
  if (compact) {
    return (
      <span className="inline-flex items-center gap-1 rounded-sm border border-accent/40 bg-accent-soft px-1.5 py-0.5 text-2xs font-semibold uppercase tracking-wide text-accent-fg">
        <FlaskConical className="h-3 w-3" aria-hidden />
        Demo data
      </span>
    );
  }
  return (
    <div className="flex items-center gap-2 border border-accent/40 bg-accent-soft px-3 py-1.5 text-xs text-accent-fg">
      <FlaskConical className="h-3.5 w-3.5 shrink-0" aria-hidden />
      <span>
        <strong>Demo data</strong> — synthetic, seeded historical data. Not real market prices or
        performance.
      </span>
    </div>
  );
}
