import type { ReactNode } from 'react';

/** Labelled form field wrapper with error text. */
export function Field({
  label,
  htmlFor,
  error,
  hint,
  children,
}: {
  label: string;
  htmlFor?: string;
  error?: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={htmlFor} className="text-xs font-medium text-content-2">
        {label}
      </label>
      {children}
      {hint && !error ? <p className="text-2xs text-content-3">{hint}</p> : null}
      {error ? (
        <p className="text-2xs text-sell" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}
