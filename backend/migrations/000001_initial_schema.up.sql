-- Gold Mine Lab initial schema.
-- IDs: human-readable slugs for assets/strategies, UUIDs for generated rows.

CREATE TABLE IF NOT EXISTS assets (
    id          TEXT PRIMARY KEY,              -- e.g. 'xauusd'
    symbol      TEXT NOT NULL,                 -- e.g. 'XAUUSD'
    name        TEXT NOT NULL,
    asset_class TEXT NOT NULL,                 -- metal | equity | forex | ...
    currency    TEXT NOT NULL,                 -- quote currency, e.g. 'USD'
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS strategies (
    id                TEXT PRIMARY KEY,        -- e.g. 'ema-cross'
    name              TEXT NOT NULL,
    version           TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    supported_assets  JSONB NOT NULL,          -- ["xauusd", ...]
    timeframe         TEXT NOT NULL,
    parameters        JSONB NOT NULL,          -- parameter schema array
    status            TEXT NOT NULL DEFAULT 'inactive',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS strategy_signals (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id TEXT NOT NULL REFERENCES strategies(id),
    asset_id    TEXT NOT NULL REFERENCES assets(id),
    timestamp   TIMESTAMPTZ NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('BUY','SELL','NEUTRAL')),
    score       DOUBLE PRECISION NOT NULL,
    price       DOUBLE PRECISION NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (strategy_id, asset_id, timestamp)  -- idempotent regeneration
);

CREATE INDEX IF NOT EXISTS idx_signals_strategy_asset_ts
    ON strategy_signals (strategy_id, asset_id, timestamp);

CREATE TABLE IF NOT EXISTS backtest_runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id     TEXT NOT NULL REFERENCES strategies(id),
    asset_id        TEXT NOT NULL REFERENCES assets(id),
    timeframe       TEXT NOT NULL,
    start_date      DATE NOT NULL,
    end_date        DATE NOT NULL,
    initial_capital DOUBLE PRECISION NOT NULL,
    position_size   DOUBLE PRECISION NOT NULL, -- % of equity deployed per trade
    commission      DOUBLE PRECISION NOT NULL, -- % of notional per fill
    slippage        DOUBLE PRECISION NOT NULL, -- % adverse adjustment per fill
    parameters      JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued','running','completed','failed')),
    equity_curve    JSONB,                     -- [{time, equity, drawdown}, ...]
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_runs_strategy_asset_created
    ON backtest_runs (strategy_id, asset_id, created_at);

CREATE TABLE IF NOT EXISTS backtest_trades (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id      UUID NOT NULL REFERENCES backtest_runs(id) ON DELETE CASCADE,
    strategy_id TEXT NOT NULL,
    asset_id    TEXT NOT NULL,
    side        TEXT NOT NULL CHECK (side IN ('BUY','SELL')),
    entry_time  TIMESTAMPTZ NOT NULL,
    entry_price DOUBLE PRECISION NOT NULL,
    exit_time   TIMESTAMPTZ,
    exit_price  DOUBLE PRECISION,
    quantity    DOUBLE PRECISION NOT NULL,
    pnl         DOUBLE PRECISION,
    status      TEXT NOT NULL DEFAULT 'closed' CHECK (status IN ('open','closed')),
    result      TEXT NOT NULL DEFAULT 'open' CHECK (result IN ('win','loss','open'))
);

CREATE INDEX IF NOT EXISTS idx_trades_run ON backtest_trades (run_id);
CREATE INDEX IF NOT EXISTS idx_trades_entry ON backtest_trades (asset_id, entry_time);

CREATE TABLE IF NOT EXISTS backtest_metrics (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id       UUID NOT NULL REFERENCES backtest_runs(id) ON DELETE CASCADE,
    metric_name  TEXT NOT NULL,
    metric_value DOUBLE PRECISION NOT NULL,
    UNIQUE (run_id, metric_name)
);

CREATE INDEX IF NOT EXISTS idx_metrics_run ON backtest_metrics (run_id);
