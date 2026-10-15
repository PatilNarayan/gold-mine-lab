-- Signal research analyses (Phase 9): a persisted, read-only cross-strategy
-- replay. The request holds the full candidate list + shared window; the
-- result holds summary statistics only — per-candle signals are never stored.
CREATE TABLE IF NOT EXISTS signal_research (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status       TEXT NOT NULL DEFAULT 'pending',  -- pending | running | completed | failed
    request      JSONB NOT NULL,                   -- candidates, window, event-study flag
    result       JSONB,                            -- stats + pairwise + forward study
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);
