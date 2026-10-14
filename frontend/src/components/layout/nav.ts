import {
  ArrowLeftRight,
  BarChart3,
  ChartCandlestick,
  FlaskConical,
  Layers,
  LayoutDashboard,
  Settings,
  type LucideIcon,
} from 'lucide-react';

export interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
}

export const NAV_ITEMS: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard, end: true },
  { to: '/market', label: 'Market', icon: ChartCandlestick },
  { to: '/strategies', label: 'Strategies', icon: Layers },
  { to: '/backtests', label: 'Backtests', icon: FlaskConical },
  { to: '/results', label: 'Results', icon: BarChart3 },
  { to: '/trades', label: 'Trades', icon: ArrowLeftRight },
  { to: '/settings', label: 'Settings', icon: Settings },
];
