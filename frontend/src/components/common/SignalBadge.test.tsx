import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { SignalBadge } from './SignalBadge';

describe('SignalBadge', () => {
  it('renders BUY with label and icon (not color alone)', () => {
    render(<SignalBadge action="BUY" />);
    expect(screen.getByText('BUY')).toBeInTheDocument();
    // icon is decorative (aria-hidden) — label carries the meaning
    expect(document.querySelector('svg[aria-hidden="true"]')).toBeTruthy();
  });

  it('renders SELL and NEUTRAL distinctly', () => {
    const { rerender } = render(<SignalBadge action="SELL" />);
    expect(screen.getByText('SELL')).toBeInTheDocument();
    rerender(<SignalBadge action="NEUTRAL" />);
    expect(screen.getByText('NEUTRAL')).toBeInTheDocument();
  });
});
