import { forwardRef, type ButtonHTMLAttributes } from 'react';

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger';

const variants: Record<Variant, string> = {
  primary:
    'bg-accent text-surface-0 font-semibold hover:brightness-110 disabled:opacity-50 disabled:hover:brightness-100',
  secondary: 'border border-line bg-surface-2 text-content-1 hover:bg-surface-3',
  ghost: 'text-content-2 hover:bg-surface-2 hover:text-content-1',
  danger: 'border border-sell/40 text-sell hover:bg-sell/10',
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: 'sm' | 'md';
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'primary', size = 'md', className = '', type = 'button', ...rest },
  ref,
) {
  const sizeCls = size === 'sm' ? 'px-2 py-1 text-xs' : 'px-3 py-1.5 text-sm';
  return (
    <button
      ref={ref}
      type={type}
      className={`inline-flex items-center gap-1.5 rounded-sm transition-colors ${sizeCls} ${variants[variant]} ${className}`}
      {...rest}
    />
  );
});
