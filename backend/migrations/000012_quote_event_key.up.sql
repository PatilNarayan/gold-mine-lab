-- Durable event identity: deterministic key over the fields that define a
-- market event — computed by Postgres at insert time (a plain column, not
-- generated: extract() is STABLE, not IMMUTABLE, so it can't drive a
-- generated column). Rows lacking a provider timestamp get event_key NULL —
-- their identity is unprovable (a resend and a genuine repeated observation
-- are indistinguishable), so they are NOT deduplicated.
ALTER TABLE quote_observations ADD COLUMN event_key TEXT;

-- Backfill existing rows with the same expression the INSERT uses.
UPDATE quote_observations
SET event_key = provider || '|' || symbol || '|' ||
                extract(epoch from provider_time)::text || '|' ||
                bid::text || '|' || ask::text || '|' || mid::text
WHERE provider_time IS NOT NULL;

-- Durable idempotency: an identical event can never be inserted twice,
-- even across process restarts or concurrent recorder paths.
CREATE UNIQUE INDEX idx_quote_observations_event_key
    ON quote_observations (event_key) WHERE event_key IS NOT NULL;
