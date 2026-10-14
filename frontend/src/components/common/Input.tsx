import { forwardRef, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from 'react';

const baseField =
  'w-full rounded-sm border border-line bg-surface-1 px-2.5 py-1.5 text-sm text-content-1 placeholder:text-content-3 focus:border-accent disabled:opacity-50';

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  function Input({ className = '', ...rest }, ref) {
    return <input ref={ref} className={`${baseField} ${className}`} {...rest} />;
  },
);

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  children: ReactNode;
}

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  { className = '', children, ...rest },
  ref,
) {
  return (
    <select ref={ref} className={`${baseField} ${className}`} {...rest}>
      {children}
    </select>
  );
});
