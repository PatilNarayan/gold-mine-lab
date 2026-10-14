import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { BacktestForm } from './BacktestForm';
import { MOCK_STRATEGIES } from '@/mock/strategies';
import { MOCK_ASSETS } from '@/mock/assets';
import { useBacktestDraftStore } from '@/stores/useBacktestDraftStore';

beforeEach(() => {
  useBacktestDraftStore.setState({ draft: {} });
});

function renderForm(onRun = vi.fn()) {
  render(
    <BacktestForm
      strategies={MOCK_STRATEGIES}
      assets={MOCK_ASSETS}
      onRun={onRun}
    />,
  );
  return onRun;
}

describe('BacktestForm', () => {
  it('renders strategy parameters dynamically from the schema', () => {
    renderForm();
    // ema-cross is the first strategy: fastLength/slowLength/direction params
    expect(screen.getByLabelText('Fast EMA')).toBeInTheDocument();
    expect(screen.getByLabelText('Slow EMA')).toBeInTheDocument();
    expect(screen.getByLabelText('Direction')).toBeInTheDocument();
  });

  it('blocks submission and shows errors when dates are invalid', async () => {
    const onRun = renderForm();
    const user = userEvent.setup();
    await user.clear(screen.getByLabelText('End date'));
    await user.type(screen.getByLabelText('End date'), '2024-01-01');
    await user.click(screen.getByRole('button', { name: /run backtest/i }));
    expect(await screen.findByText(/end date must be after start date/i)).toBeInTheDocument();
    expect(onRun).not.toHaveBeenCalled();
  });

  it('submits a valid request', async () => {
    const onRun = renderForm();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /run backtest/i }));
    expect(onRun).toHaveBeenCalledOnce();
    const req = onRun.mock.calls[0][0];
    expect(req.strategyId).toBe('ema-cross');
    expect(req.assetId).toBeTruthy();
    expect(req.parameters.fastLength).toBe(20);
  });
});
