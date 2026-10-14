export function Skeleton({ className = '' }: { className?: string }) {
  return (
    <div
      className={`animate-pulse rounded-sm bg-surface-3 ${className}`}
      role="status"
      aria-label="Loading"
    />
  );
}

export function SkeletonRows({ rows = 5, height = 'h-8' }: { rows?: number; height?: string }) {
  return (
    <div className="space-y-2" aria-busy="true">
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} className={`w-full ${height}`} />
      ))}
    </div>
  );
}
