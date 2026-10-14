import { NavLink } from 'react-router-dom';
import { NAV_ITEMS } from './nav';

/**
 * Desktop sidebar: full width at lg+, collapses to an icon rail at md.
 * Keyboard-navigable via native links with visible focus rings.
 */
export function Sidebar() {
  return (
    <nav
      aria-label="Primary"
      className="hidden w-14 shrink-0 flex-col border-r border-line bg-surface-1 py-3 md:flex lg:w-52"
    >
      <ul className="flex flex-col gap-0.5 px-2">
        {NAV_ITEMS.map(({ to, label, icon: Icon, end }) => (
          <li key={to}>
            <NavLink
              to={to}
              end={end}
              title={label}
              className={({ isActive }) =>
                `flex items-center gap-3 rounded-sm px-2.5 py-2 text-sm transition-colors lg:px-3 ${
                  isActive
                    ? 'bg-accent-soft text-accent-fg'
                    : 'text-content-2 hover:bg-surface-2 hover:text-content-1'
                }`
              }
            >
              <Icon className="h-4 w-4 shrink-0" aria-hidden />
              <span className="hidden lg:inline">{label}</span>
            </NavLink>
          </li>
        ))}
      </ul>
      <div className="mt-auto px-3 text-2xs text-content-3">
        <span className="hidden lg:block">Research · Backtest · Iterate</span>
      </div>
    </nav>
  );
}
