ALTER TABLE shadow_decisions
    DROP CONSTRAINT shadow_decisions_session_bar_uq;
ALTER TABLE shadow_decisions
    ADD CONSTRAINT shadow_decisions_strategy_id_strategy_version_symbol_timefr_key
    UNIQUE (strategy_id, strategy_version, symbol, timeframe, bar_time);

ALTER TABLE shadow_positions DROP CONSTRAINT shadow_positions_pkey;
ALTER TABLE shadow_positions ALTER COLUMN session_id DROP NOT NULL;
ALTER TABLE shadow_positions
    ADD PRIMARY KEY (strategy_id, strategy_version, symbol, timeframe);
