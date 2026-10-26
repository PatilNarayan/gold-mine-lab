-- Shadow decision ledger: append-only evidence that each eligible closed
-- candle was actually evaluated. A moving last_bar_time marker alone does
-- not prove evaluation succeeded — this records the strategy's verdict,
-- indicator values, and actionability for every close processed.
--
--   action        — the strategy's raw verdict (buy|sell|neutral).
--   actionable    — whether it produced an order (state-gated).
--   blocked_reason— why not actionable when it wasn't.
--   source        — 'live' (post-startup confirmed close) vs 'recovery'
--                   (downtime-missed close, replayed) — never conflated.
CREATE TABLE shadow_decisions (
    id               BIGSERIAL PRIMARY KEY,
    session_id       BIGINT REFERENCES shadow_sessions(id),
    order_id         BIGINT REFERENCES shadow_orders(id),
    strategy_id      TEXT NOT NULL,
    strategy_version TEXT NOT NULL,
    symbol           TEXT NOT NULL,
    timeframe        TEXT NOT NULL,
    bar_time         TIMESTAMPTZ NOT NULL,
    decided_at       TIMESTAMPTZ NOT NULL,
    source           TEXT NOT NULL CHECK (source IN ('live','recovery')),
    action           TEXT NOT NULL CHECK (action IN ('buy','sell','neutral')),
    reason           TEXT,
    fast_ema         DOUBLE PRECISION,
    slow_ema         DOUBLE PRECISION,
    actionable       BOOLEAN NOT NULL DEFAULT false,
    blocked_reason   TEXT,
    -- Decision→order linkage invariant: an actionable decision must link
    -- the order it produced. Neutral/blocked rows carry no order.
    CHECK (actionable = false OR order_id IS NOT NULL),
    UNIQUE (strategy_id, strategy_version, symbol, timeframe, bar_time)
);
CREATE INDEX idx_shadow_decisions_session ON shadow_decisions (session_id, bar_time);
