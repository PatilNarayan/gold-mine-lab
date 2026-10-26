-- Backtest data provenance: which provider's candles the run consumed, how
-- many, and over what actual span — separate from the requested date range.

ALTER TABLE backtest_runs
    ADD COLUMN IF NOT EXISTS data_provider  TEXT,
    ADD COLUMN IF NOT EXISTS data_candles   INTEGER,
    ADD COLUMN IF NOT EXISTS data_first_bar TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS data_last_bar  TIMESTAMPTZ;

-- Legacy provenance: every run predating the provider-backed store
-- (2026-10-23) executed against the CSV dataset — provable because the
-- DBProvider did not exist before then. Runs with no fingerprint get
-- 'unknown', never a guess. New runs record the provider explicitly.
UPDATE backtest_runs
SET data_provider = CASE
    WHEN created_at < '2026-10-23'::timestamptz THEN
        CASE WHEN dataset IS NOT NULL THEN 'csv' ELSE 'unknown' END
    ELSE 'tiingo'
END
WHERE data_provider IS NULL;
