import { AlertTriangle, RotateCcw } from 'lucide-react';
import { ApiError } from '@/types/api';
import { Button } from './Button';

export function ErrorState({
  error,
  onRetry,
  title = 'Failed to load data',
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
}) {
  const message =
    error instanceof ApiError
      ? `${error.message}${error.requestId !== 'local' ? ` (request ${error.requestId})` : ''}`
      : error instanceof Error
        ? error.message
        : 'Unknown error';
  return (
    <div
      className="flex flex-col items-center justify-center gap-3 border border-sell/30 bg-sell/5 px-6 py-10 text-center"
      role="alert"
    >
      <AlertTriangle className="h-6 w-6 text-sell" aria-hidden />
      <div>
        <p className="text-sm font-medium text-content-1">{title}</p>
        <p className="mt-1 max-w-md text-xs text-content-3">{message}</p>
      </div>
      {onRetry ? (
        <Button variant="secondary" onClick={onRetry}>
          <RotateCcw className="h-3.5 w-3.5" aria-hidden />
          Retry
        </Button>
      ) : null}
    </div>
  );
}
