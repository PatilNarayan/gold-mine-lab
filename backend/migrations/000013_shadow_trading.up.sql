-- Shadow trading: hypothetical executions against real quote events.
-- orders = one row per signal→order decision; positions = one row per
-- strategy/symbol/timeframe holding current state + realized aggregates.
-- Everything is SIMULATED — no broker, no real money.
CREATE TABLE shadow_orders (
    id                   BIGSERIAL PRIMARY KEY,
    order_key            TEXT NOT NULL UNIQUE,  -- idempotency: strategy|symbol|tf|bar_time|side
    strategy_id          TEXT NOT NULL,
    strategy_version     TEXT NOT NULL,
    symbol               TEXT NOT NULL,
    timeframe            TEXT NOT NULL,
    side                 TEXT NOT NULL CHECK (side IN ('BUY','SELL')),
    signal_bar_time      TIMESTAMPTZ NOT NULL,   -- the confirmed-closed candle that produced the signal
    decision_at          TIMESTAMPTZ NOT NULL,   -- when the engine acted on it
    status               TEXT NOT NULL CHECK (status IN ('pending','filled','rejected','expired')),
    reject_reason        TEXT,
    quote_provider_time  TIMESTAMPTZ,
    quote_received_at    TIMESTAMPTZ,
    quote_bid            DOUBLE PRECISION,
    quote_ask            DOUBLE PRECISION,
    quote_mid            DOUBLE PRECISION,
    fill_price           DOUBLE PRECISION,
    filled_at            TIMESTAMPTZ,
    quantity             DOUBLE PRECISION,
    commission           DOUBLE PRECISION,      -- configured assumption, not measured
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_shadow_orders_status ON shadow_orders (status) WHERE status <> 'filled';

-- One row per (strategy, symbol, timeframe): position state + restart
-- recovery (last processed candle) + realized aggregates.
CREATE TABLE shadow_positions (
    strategy_id       TEXT NOT NULL,
    strategy_version  TEXT NOT NULL,
    symbol            TEXT NOT NULL,
    timeframe         TEXT NOT NULL,
    side              TEXT,                       -- NULL = flat
    quantity          DOUBLE PRECISION NOT NULL DEFAULT 0,
    entry_price       DOUBLE PRECISION,
    entry_at          TIMESTAMPTZ,
    entry_order_id    BIGINT,
    entry_commission  DOUBLE PRECISION NOT NULL DEFAULT 0,  -- recorded at entry fill for exact net P&L on exit
    realized_pnl      DOUBLE PRECISION NOT NULL DEFAULT 0,
    realized_comm     DOUBLE PRECISION NOT NULL DEFAULT 0,  -- accumulated commission assumption
    realized_spread   DOUBLE PRECISION NOT NULL DEFAULT 0,  -- accumulated measured spread cost vs mid
    last_bar_time     TIMESTAMPTZ,               -- newest processed confirmed-closed candle (restart resume)
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (strategy_id, strategy_version, symbol, timeframe)
);
