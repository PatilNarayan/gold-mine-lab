import { Navigate } from 'react-router-dom';
import { useLatestRunId } from '@/hooks';
import { SkeletonRows } from '@/components/common/Skeleton';
import { ErrorState } from '@/components/common/ErrorState';
import { EmptyState } from '@/components/common/EmptyState';
import { Button } from '@/components/common/Button';
import { Link } from 'react-router-dom';

/** `/results` (no id) redirects to the latest completed run. */
export default function ResultsRedirect() {
  const latest = useLatestRunId();

  if (latest.isLoading) return <SkeletonRows rows={3} height="h-12" />;
  if (latest.isError) return <ErrorState error={latest.error} onRetry={() => latest.refetch()} />;
  if (!latest.data) {
    return (
      <EmptyState
        title="No completed runs"
        message="Run a backtest first — results will appear here."
        action={
          <Link to="/backtests">
            <Button variant="secondary" size="sm">
              Go to Backtests
            </Button>
          </Link>
        }
      />
    );
  }
  return <Navigate to={`/results/${latest.data}`} replace />;
}
