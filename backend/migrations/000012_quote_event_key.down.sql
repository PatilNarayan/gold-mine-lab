DROP INDEX IF EXISTS idx_quote_observations_event_key;
ALTER TABLE quote_observations DROP COLUMN IF EXISTS event_key;
