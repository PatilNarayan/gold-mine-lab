import { useRef } from 'react';
import { NavLink } from 'react-router-dom';
import { X } from 'lucide-react';
import { useUiStore } from '@/stores/useUiStore';
import { useModalLayer } from '@/hooks/useModalLayer';
import { NAV_ITEMS } from './nav';

/** Drawer navigation below `md` breakpoint. Modal: focus is contained inside
 *  the drawer, background scroll is locked, Escape closes, and focus returns
 *  to the control that opened it. */
export function MobileNav() {
  const open = useUiStore((s) => s.mobileNavOpen);
  const setOpen = useUiStore((s) => s.setMobileNavOpen);
  const navRef = useRef<HTMLElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  useModalLayer({
    open,
    containerRef: navRef,
    onClose: () => setOpen(false),
    initialFocusRef: closeRef,
  });

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 md:hidden" role="dialog" aria-modal="true" aria-label="Navigation">
      <div className="absolute inset-0 bg-black/60" onClick={() => setOpen(false)} />
      <nav ref={navRef} className="absolute left-0 top-0 h-full w-64 border-r border-line bg-surface-1 py-3">
        <div className="mb-2 flex items-center justify-between px-4">
          <span className="text-sm font-semibold text-content-1">Gold Mine Lab</span>
          <button
            ref={closeRef}
            type="button"
            aria-label="Close navigation"
            onClick={() => setOpen(false)}
            className="flex h-10 w-10 items-center justify-center rounded-sm text-content-2 hover:bg-surface-2"
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
