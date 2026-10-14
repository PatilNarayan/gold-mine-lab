import { ArrowDownRight, ArrowUpRight, Minus } from 'lucide-react';
import type { SignalAction } from '@/types/domain';
import { Badge } from './Badge';

const config: Record<
  SignalAction,
  { tone: 'buy' | 'sell' | 'neutral'; icon: typeof ArrowUpRight; label: string }
> = {
  BUY: { tone: 'buy', icon: ArrowUpRight, label: 'BUY' },
  SELL: { tone: 'sell', icon: ArrowDownRight, label: 'SELL' },
  NEUTRAL: { tone: 'neutral', icon: Minus, label: 'NEUTRAL' },
};

/**
 * Signal badge — never relies on color alone: pairs tone with a text label
 * and a directional icon for accessibility.
 */
export function SignalBadge({ action }: { action: SignalAction }) {
  const { tone, icon: Icon, label } = config[action];
  return (
    <Badge tone={tone}>
      <Icon className="h-3 w-3" aria-hidden />
      <span>{label}</span>
    </Badge>
  );
}
