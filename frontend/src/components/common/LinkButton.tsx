import { Link, type LinkProps } from 'react-router-dom';
import type { ReactNode } from 'react';

const variants: Record<'primary' | 'secondary', string> = {
  primary: 'bg-accent text-surface-0 font-semibold hover:brightness-110',
  secondary: 'border border-line bg-surface-2 text-content-1 hover:bg-surface-3',
};

/** Anchor styled like the Button component's variants — for CTAs that are
 *  navigation, not actions. */
export function LinkButton({
  variant = 'primary',
  size = 'md',
  className = '',
  children,
  ...rest
}: LinkProps & { variant?: 'primary' | 'secondary'; size?: 'sm' | 'md'; children: ReactNode }) {
  const sizeCls = size === 'sm' ? 'px-2 py-1 text-xs' : 'px-3 py-1.5 text-sm';
  return (
    <Link
      className={`inline-flex items-center gap-1.5 rounded-sm transition-colors ${sizeCls} ${variants[variant]} ${className}`}
      {...rest}
    >
      {children}
    </Link>
  );
}
