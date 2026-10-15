import { lazy } from 'react';
import { createBrowserRouter } from 'react-router-dom';
import { AppShell } from '@/components/layout/AppShell';
import NotFound from '@/pages/NotFound';

// Route components are lazy-loaded; chart libraries stay out of the initial
// bundle because they are only imported by lazily-loaded pages.
const Dashboard = lazy(() => import('@/pages/Dashboard'));
const Market = lazy(() => import('@/pages/Market'));
const Strategies = lazy(() => import('@/pages/Strategies'));
const StrategyDetail = lazy(() => import('@/pages/StrategyDetail'));
const Backtests = lazy(() => import('@/pages/Backtests'));
const Compare = lazy(() => import('@/pages/Compare'));
const Results = lazy(() => import('@/pages/Results'));
const ResultsRedirect = lazy(() => import('@/pages/ResultsRedirect'));
const Trades = lazy(() => import('@/pages/Trades'));
const Settings = lazy(() => import('@/pages/Settings'));

export const router = createBrowserRouter([
  {
    element: <AppShell />,
    children: [
      { path: '/', element: <Dashboard /> },
      { path: '/market', element: <Market /> },
      { path: '/strategies', element: <Strategies /> },
      { path: '/strategies/:id', element: <StrategyDetail /> },
      { path: '/backtests', element: <Backtests /> },
      { path: '/compare', element: <Compare /> },
      { path: '/compare/:id', element: <Compare /> },
      { path: '/results', element: <ResultsRedirect /> },
      { path: '/results/:runId', element: <Results /> },
      { path: '/trades', element: <Trades /> },
      { path: '/settings', element: <Settings /> },
      { path: '*', element: <NotFound /> },
    ],
  },
]);
