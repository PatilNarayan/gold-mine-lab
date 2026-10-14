type Status = 'ok' | 'warn' | 'error' | 'muted';

const colors: Record<Status, string> = {
  ok: 'bg-buy',
  warn: 'bg-warning',
  error: 'bg-sell',
  muted: 'bg-content-3',
};

export function StatusDot({ status, label }: { status: Status; label?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5" role={label ? 'status' : undefined}>
      <span className={`h-1.5 w-1.5 rounded-full ${colors[status]}`} aria-hidden />
      {label ? <span className="text-xs text-content-2">{label}</span> : null}
    </span>
  );
}
