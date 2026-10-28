-- Session-scoped state isolation. Positions, decisions, and order
-- idempotency must not collide across sessions — a new configuration
-- session starts clean instead of inheriting a prior session's
-- position/marker/P&L, and records remain auditable per session.
ALTER TABLE shadow_positions DROP CONSTRAINT shadow_positions_pkey;
UPDATE shadow_positions
   SET session_id = (SELECT max(id) FROM shadow_sessions)
 WHERE session_id IS NULL;
ALTER TABLE shadow_positions ALTER COLUMN session_id SET NOT NULL;
ALTER TABLE shadow_positions
    ADD PRIMARY KEY (session_id, strategy_id, strategy_version, symbol, timeframe);

ALTER TABLE shadow_decisions
    DROP CONSTRAINT shadow_decisions_strategy_id_strategy_version_symbol_timefr_key;
ALTER TABLE shadow_decisions
    ADD CONSTRAINT shadow_decisions_session_bar_uq
    UNIQUE NULLS NOT DISTINCT
    (session_id, strategy_id, strategy_version, symbol, timeframe, bar_time);
