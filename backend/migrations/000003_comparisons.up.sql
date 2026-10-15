-- Phase 4: research comparisons. A comparison groups 2+ strategy runs
-- executed under identical assumptions (window, capital, costs, dataset,
-- engine version), optionally with a later out-of-sample leg.

CREATE TABLE comparisons (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id        TEXT NOT NULL REFERENCES assets(id),
    timeframe       TEXT NOT NULL,
    start_date      DATE NOT NULL,          -- in-sample window
    end_date        DATE NOT NULL,
    oos_start_date  DATE,                   -- optional out-of-sample window
    oos_end_date    DATE,
    initial_capital DOUBLE PRECISION NOT NULL,
    position_size   DOUBLE PRECISION NOT NULL,
    commission      DOUBLE PRECISION NOT NULL,
    slippage        DOUBLE PRECISION NOT NULL,
    strategy_ids    JSONB NOT NULL,         -- ordered leg strategy IDs
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','running','completed','partial_failed','failed')),
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);

-- Member runs: which comparison produced them, and which sample window.
ALTER TABLE backtest_runs
    ADD COLUMN comparison_id UUID REFERENCES comparisons(id),
    ADD COLUMN sample TEXT CHECK (sample IS NULL OR sample IN ('in_sample','out_of_sample'));

CREATE INDEX idx_runs_comparison ON backtest_runs(comparison_id)
    WHERE comparison_id IS NOT NULL;
