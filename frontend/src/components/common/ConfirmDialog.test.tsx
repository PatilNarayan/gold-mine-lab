import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ConfirmDialog } from './ConfirmDialog';

function setup(overrides: Partial<Parameters<typeof ConfirmDialog>[0]> = {}) {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  const props = {
    open: true,
    title: 'Freeze study',
    message: 'This cannot be undone.',
    onConfirm,
    onCancel,
    ...overrides,
  };
  render(
    <>
      <button type="button">before</button>
      <ConfirmDialog {...props} />
      <button type="button">after</button>
    </>,
  );
  return { onConfirm, onCancel };
}

describe('ConfirmDialog', () => {
  it('renders a named modal dialog', () => {
    setup();
    const dlg = screen.getByRole('dialog', { name: 'Freeze study' });
    expect(dlg).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByText('This cannot be undone.')).toBeInTheDocument();
  });

  it('focuses the confirm action on open', () => {
    setup();
    expect(screen.getByRole('button', { name: 'Confirm' })).toHaveFocus();
  });

  it('confirm fires onConfirm; backdrop click and Escape cancel', async () => {
    const { onConfirm, onCancel } = setup();
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    expect(onConfirm).toHaveBeenCalledOnce();
    await userEvent.keyboard('{Escape}');
    expect(onCancel).toHaveBeenCalled();
  });

  it('cancel button calls onCancel, not onConfirm', async () => {
    const { onConfirm, onCancel } = setup();
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalledOnce();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('renders nothing when closed', () => {
    setup({ open: false });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
