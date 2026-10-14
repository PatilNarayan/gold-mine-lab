import { Suspense, useEffect } from 'react';
import { Outlet } from 'react-router-dom';
import { useUiStore } from '@/stores/useUiStore';
import { Skeleton } from '@/components/common/Skeleton';
import { Header } from './Header';
import { Sidebar } from './Sidebar';
import { MobileNav } from './MobileNav';

function RouteFallback() {
  return (
    <div className="space-y-3" aria-label="Loading page">
      <Skeleton className="h-8 w-1/3" />
      <Skeleton className="h-40 w-full" />
      <Skeleton className="h-40 w-full" />
    </div>
  );
}

/** App chrome: sidebar + header + routed content area. */
export function AppShell() {
  const theme = useUiStore((s) => s.theme);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  return (
    <div className="flex h-full">
      <Sidebar />
      <MobileNav />
      <div className="flex min-w-0 flex-1 flex-col">
        <Header />
        <main className="flex-1 overflow-y-auto p-3 sm:p-4" id="main">
          <Suspense fallback={<RouteFallback />}>
            <Outlet />
          </Suspense>
        </main>
      </div>
    </div>
  );
}
