ALTER TABLE candle_sync
    DROP COLUMN IF EXISTS state,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS retry_at,
    DROP COLUMN IF EXISTS progress_bar,
    DROP COLUMN IF EXISTS progress_dir;
