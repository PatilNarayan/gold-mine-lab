ALTER TABLE quote_coverage
    DROP COLUMN IF EXISTS last_feed_degraded_at,
    DROP COLUMN IF EXISTS last_heartbeat_gap_at,
    DROP COLUMN IF EXISTS last_drop_at,
    DROP COLUMN IF EXISTS feed_state;
