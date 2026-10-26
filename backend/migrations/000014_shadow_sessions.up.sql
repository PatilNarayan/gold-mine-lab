-- Shadow sessions: an auditable identity for one engine configuration's
-- forward run. A restart RESUMES the same session (same identity, open
-- row) — it never silently resets state. A changed configuration starts a
-- clearly distinguished new session.
CREATE TABLE shadow_sessions (
    id               BIGSERIAL PRIMARY KEY,
    strategy_id      TEXT NOT NULL,
    strategy_version TEXT NOT NULL,
    symbol           TEXT NOT NULL,
    timeframe        TEXT NOT NULL,
    provider         TEXT NOT NULL,
    config           JSONB NOT NULL,         -- frozen engine+policy config
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),  -- first start
    last_active_at   TIMESTAMPTZ NOT NULL DEFAULT now(),  -- touched on resume/activity
    restart_count    INTEGER NOT NULL DEFAULT 0,          -- increments on each resume
    ended_at         TIMESTAMPTZ                          -- NULL = session open
);
-- One open session per identity at a time.
CREATE UNIQUE INDEX idx_shadow_sessions_open
    ON shadow_sessions (strategy_id, strategy_version, symbol, timeframe, provider)
    WHERE ended_at IS NULL;

-- Orders/decisions carry the session they were produced under.
ALTER TABLE shadow_orders ADD COLUMN session_id BIGINT REFERENCES shadow_sessions(id);
ALTER TABLE shadow_positions ADD COLUMN session_id BIGINT REFERENCES shadow_sessions(id);

-- 'missed': the signal existed but the execution window passed while the
-- engine was offline and no admissible quote evidence exists — distinct
-- from 'expired' (order was live and timed out) and 'rejected' (policy).
ALTER TABLE shadow_orders DROP CONSTRAINT shadow_orders_status_check;
ALTER TABLE shadow_orders ADD CONSTRAINT shadow_orders_status_check
    CHECK (status IN ('pending','filled','rejected','expired','missed'));
