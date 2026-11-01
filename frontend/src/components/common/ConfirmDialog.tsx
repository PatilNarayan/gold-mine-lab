import { useRef } from 'react';
import { AlertTriangle } from 'lucide-react';
import { useModalLayer } from '@/hooks/useModalLayer';
import { Button } from './Button';

/**
 * Accessible confirmation dialog for consequential actions. Modal semantics:
 * focus is trapped, Escape cancels, focus returns to the opener on close.
 * `onConfirm` only fires from the explicit confirm button — never on
 * backdrop dismissal — so consequential writes need a deliberate click.
 */
export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  tone = 'warning',
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: 'warning' | 'danger';
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  useModalLayer({ open, containerRef, onClose: onCancel, initialFocusRef: confirmRef });

  if (!open) return null;

  const iconCls = tone === 'danger' ? 'text-sell' : 'text-warning';
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/60" onClick={onCancel} aria-hidden />
      <div
        ref={containerRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="relative w-full max-w-md rounded-sm border border-line bg-surface-1 p-4 shadow-lg"
      >
        <div className="flex items-start gap-3">
          <AlertTriangle className={`mt-0.5 h-5 w-5 shrink-0 ${iconCls}`} aria-hidden />
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-content-1">{title}</h2>
            <p className="mt-1.5 text-xs text-content-2">{message}</p>
          </div>
        </div>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="secondary" size="sm" onClick={onCancel}>
            {cancelLabel}
          </Button>
          <Button
            ref={confirmRef}
            variant={tone === 'danger' ? 'danger' : 'primary'}
            size="sm"
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
