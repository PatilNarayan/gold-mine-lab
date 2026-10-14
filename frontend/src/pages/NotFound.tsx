import { Link } from 'react-router-dom';
import { Compass } from 'lucide-react';

export default function NotFound() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
      <Compass className="h-8 w-8 text-content-3" aria-hidden />
      <h1 className="text-lg font-semibold text-content-1">404 — page not found</h1>
      <p className="max-w-sm text-xs text-content-3">
        The route you requested does not exist in Gold Mine Lab.
      </p>
      <Link to="/" className="text-xs text-accent-fg hover:underline">
        ← Back to dashboard
      </Link>
    </div>
  );
}
