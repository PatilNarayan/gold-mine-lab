import { Inbox } from 'lucide-react';
import type { ReactNode } from 'react';

export function EmptyState({
  title = 'Nothing here yet',
  message,
  action,
}: {
  title?: string;
  message?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 border border-dashed border-line px-6 py-12 text-center">
      <Inbox className="h-6 w-6 text-content-3" aria-hidden />
      <p className="text-sm font-medium text-content-1">{title}</p>
      {message ? <p className="max-w-md text-xs text-content-3">{message}</p> : null}
      {action}
    </div>
  );
}
