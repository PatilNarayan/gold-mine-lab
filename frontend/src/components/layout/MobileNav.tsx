import { NavLink } from 'react-router-dom';
import { X } from 'lucide-react';
import { useUiStore } from '@/stores/useUiStore';
import { NAV_ITEMS } from './nav';

/** Drawer navigation below `md` breakpoint. */
export function MobileNav() {
  const open = useUiStore((s) => s.mobileNavOpen);
  const setOpen = useUiStore((s) => s.setMobileNavOpen);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 md:hidden" role="dialog" aria-modal="true" aria-label="Navigation">
      <div className="absolute inset-0 bg-black/60" onClick={() => setOpen(false)} />
      <nav className="absolute left-0 top-0 h-full w-64 border-r border-line bg-surface-1 py-3">
        <div className="mb-2 flex items-center justify-between px-4">
          <span className="text-sm font-semibold text-content-1">Gold Mine Lab</span>
          <button
            type="button"
            aria-label="Close navigation"
            onClick={() => setOpen(false)}
            className="rounded-sm p-1.5 text-content-2 hover:bg-surface-2"
          >
            <X className="h-4 w-4" aria-hidden />
          </button>
        </div>
        <ul className="flex flex-col gap-0.5 px-2">
          {NAV_ITEMS.map(({ to, label, icon: Icon, end }) => (
            <li key={to}>
              <NavLink
                to={to}
                end={end}
                onClick={() => setOpen(false)}
                className={({ isActive }) =>
                  `flex items-center gap-3 rounded-sm px-3 py-2 text-sm ${
                    isActive
                      ? 'bg-accent-soft text-accent-fg'
                      : 'text-content-2 hover:bg-surface-2 hover:text-content-1'
                  }`
                }
              >
                <Icon className="h-4 w-4" aria-hidden />
                {label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
    </div>
  );
}
