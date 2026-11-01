import type { ReactNode } from 'react';

/**
 * Consistent page header: real heading hierarchy (h1), a one-line statement
 * of what the page is for, and an optional context/actions slot on the right.
 */
export function PageHeader({
  title,
  description,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
      <div className="min-w-0">
        <h1 className="text-base font-semibold text-content-1">{title}</h1>
        {description ? <p className="mt-0.5 max-w-3xl text-xs text-content-3">{description}</p> : null}
      </div>
      {children ? <div className="flex items-center gap-2">{children}</div> : null}
    </div>
  );
}
