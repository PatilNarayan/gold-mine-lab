-- Quote-observation coverage: proves the recorder was alive and persisting
-- during an interval — the difference between "no qualifying quote existed"
-- and "we can't know". Heartbeats are independent of quote arrivals: an
-- interval with zero quotes still has full coverage if the recorder ran.
CREATE TABLE quote_coverage (
    id               BIGSERIAL PRIMARY KEY,
    provider         TEXT NOT NULL,
    symbol           TEXT NOT NULL,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),  -- recorder start
    last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- periodic aliveness
    last_obs_at      TIMESTAMPTZ,                         -- last persisted obs receipt
    obs_count        BIGINT NOT NULL DEFAULT 0,
    dropped_batches  INTEGER NOT NULL DEFAULT 0,          -- failed persistence attempts
    ended_at         TIMESTAMPTZ                          -- NULL while recorder runs
);
CREATE INDEX idx_quote_coverage_span
    ON quote_coverage (provider, symbol, started_at, last_heartbeat_at);

-- 'unobservable': the execution window's quote coverage is incomplete or
-- unknown — distinct from 'missed' (coverage proven, no admissible quote).
ALTER TABLE shadow_orders DROP CONSTRAINT shadow_orders_status_check;
ALTER TABLE shadow_orders ADD CONSTRAINT shadow_orders_status_check
    CHECK (status IN ('pending','filled','rejected','expired','missed','unobservable'));
