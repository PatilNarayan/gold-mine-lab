-- Feed transition events: genuine provider state transitions persisted AT
-- EVENT TIME (not heartbeat-sampled). A heartbeat can only sample; a real
-- transition event is the only honest record that a disconnect happened.
--
--   at    — when the transition was detected/recorded.
--   since — the degraded interval's start bound: last proven-healthy
--           evidence before detection (quote receipt or last recovery).
--           A disconnect "detected at T" means the outage began somewhere
--           in (last_fresh_evidence, T] — `since` is that honest bound.
--   kind  — 'disconnect'|'degraded' open a degraded interval;
--           'recovered' closes the most recent open one;
--           'persist_fail' is a point-in-time persistence failure.
CREATE TABLE feed_events (
    id          BIGSERIAL PRIMARY KEY,
    coverage_id BIGINT NOT NULL REFERENCES quote_coverage(id),
    provider    TEXT NOT NULL,
    symbol      TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('disconnect','degraded','recovered','persist_fail')),
    at          TIMESTAMPTZ NOT NULL,
    since       TIMESTAMPTZ,
    detail      TEXT
);
CREATE INDEX idx_feed_events_coverage ON feed_events (coverage_id, kind, at);
