-- Sync reliability: per-timeframe state, error memory, retry eligibility,
-- and a resumable progress checkpoint inside the currently syncing window.
-- Coverage (first_bar..last_bar) still records only verified synced spans.

ALTER TABLE candle_sync
    ADD COLUMN IF NOT EXISTS state        TEXT NOT NULL DEFAULT 'complete',
    ADD COLUMN IF NOT EXISTS last_error   TEXT,
    ADD COLUMN IF NOT EXISTS retry_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS progress_bar TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS progress_dir TEXT;

COMMENT ON COLUMN candle_sync.state IS
  'complete | partial | rate_limited | failed — state of the LAST sync attempt';
COMMENT ON COLUMN candle_sync.progress_bar IS
  'farthest contiguous bar persisted within the in-flight extension window';
COMMENT ON COLUMN candle_sync.progress_dir IS
  'extend_first | extend_last — which edge the in-flight sync advances';
