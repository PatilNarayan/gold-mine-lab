import { Suspense } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';

// Charts can't paint in jsdom (no canvas) — stub them so route smoke tests
// verify page logic, data flow and error states instead.
vi.mock('@/components/charts/CandlestickChart', () => ({
  CandlestickChart: () => <div data-testid="candlestick-chart" />,
}));
vi.mock('@/components/charts/EquityCurveChart', () => ({
  EquityCurveChart: () => <div data-testid="equity-chart" />,
}));
vi.mock('@/components/charts/DrawdownChart', () => ({
  DrawdownChart: () => <div data-testid="drawdown-chart" />,
}));

import Dashboard from '@/pages/Dashboard';
import Market from '@/pages/Market';
import Strategies from '@/pages/Strategies';
import StrategyDetail from '@/pages/StrategyDetail';
import Backtests from '@/pages/Backtests';
import Results from '@/pages/Results';
import Trades from '@/pages/Trades';
import Settings from '@/pages/Settings';
import NotFound from '@/pages/NotFound';

function renderRoute(entry: string, routePath: string, element: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[entry]}>
        <Suspense fallback={null}>
          <Routes>
            <Route path={routePath} element={element} />
          </Routes>
        </Suspense>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('route smoke tests (mock data mode)', () => {
  it('Dashboard renders market overview, signals and equity chart', async () => {
    renderRoute('/', '/', <Dashboard />);
    expect(await screen.findByText('Market overview', {}, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByText('Historical / Demo')).toBeInTheDocument();
    expect(screen.getAllByText(/demo data/i).length).toBeGreaterThan(0);
    expect(await screen.findByTestId('equity-chart')).toBeInTheDocument();
  });

  it('Market renders candlestick chart', async () => {
    renderRoute('/market', '/market', <Market />);
    expect(await screen.findByTestId('candlestick-chart', {}, { timeout: 5000 })).toBeInTheDocument();
  });

  it('Strategies lists strategies', async () => {
    renderRoute('/strategies', '/strategies', <Strategies />);
    expect(await screen.findByText('EMA Crossover', {}, { timeout: 5000 })).toBeInTheDocument();
  });

  it('StrategyDetail renders a known strategy', async () => {
    renderRoute('/strategies/ema-cross', '/strategies/:id', <StrategyDetail />);
    expect(
      await screen.findByText('EMA Crossover', {}, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(screen.getByText('Parameters')).toBeInTheDocument();
  });

  it('Backtests renders the form and run history', async () => {
    renderRoute('/backtests', '/backtests', <Backtests />);
    expect(
      await screen.findByRole('button', { name: /run backtest/i }, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(await screen.findByText('Run history')).toBeInTheDocument();
  });

  it('Results renders a completed run with charts and trades', async () => {
    renderRoute('/results/run-001', '/results/:runId', <Results />);
    expect(await screen.findByTestId('equity-chart', {}, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByTestId('drawdown-chart')).toBeInTheDocument();
    expect(screen.getByText('Net P&L')).toBeInTheDocument();
  });

  it('Results shows an error for an unknown runId', async () => {
    renderRoute('/results/does-not-exist', '/results/:runId', <Results />);
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument(), { timeout: 5000 });
  });

  it('Results shows an error state for a failed run', async () => {
    renderRoute('/results/run-007', '/results/:runId', <Results />);
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument(), { timeout: 5000 });
    expect(screen.getByText(/failed/i)).toBeInTheDocument();
  });

  it('Trades renders filters and a paginated table', async () => {
    renderRoute('/trades?result=win', '/trades', <Trades />);
    expect(await screen.findByText(/trades · page/i, {}, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByLabelText('Strategy')).toBeInTheDocument();
  });

  it('Settings renders endpoint and theme controls', async () => {
    renderRoute('/settings', '/settings', <Settings />);
    expect(await screen.findByLabelText('API URL', {}, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByText('mock', { selector: 'span' })).toBeInTheDocument();
  });

  it('NotFound renders 404', async () => {
    renderRoute('/nope', '*', <NotFound />);
    expect(await screen.findByText(/404/)).toBeInTheDocument();
  });
});
