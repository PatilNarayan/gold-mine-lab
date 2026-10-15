-- Phase 5: fixed-configuration walk-forward research. An experiment replays
-- each configured strategy over rolling (in-sample, out-of-sample) folds;
-- every member run is a normal backtest_runs row linked by the fold rows.

CREATE TABLE walk_forward_experiments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id        TEXT NOT NULL REFERENCES assets(id),
    timeframe       TEXT NOT NULL,
    range_start     DATE NOT NULL,
    range_end       DATE NOT NULL,
    is_months       INTEGER NOT NULL,
    oos_months      INTEGER NOT NULL,
    step_months     INTEGER NOT NULL,
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

-- One fold row per (fold_index, strategy): boundaries fixed at creation,
-- member run IDs filled in as each leg executes.
CREATE TABLE walk_forward_folds (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    experiment_id   UUID NOT NULL REFERENCES walk_forward_experiments(id) ON DELETE CASCADE,
    fold_index      INTEGER NOT NULL,
    strategy_id     TEXT NOT NULL,
    is_start        DATE NOT NULL,
    is_end          DATE NOT NULL,
    oos_start       DATE NOT NULL,
    oos_end         DATE NOT NULL,
    is_run_id       UUID REFERENCES backtest_runs(id),
    oos_run_id      UUID REFERENCES backtest_runs(id),
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','completed','failed')),
    error           TEXT,
    UNIQUE (experiment_id, fold_index, strategy_id)
);

CREATE INDEX idx_wf_folds_experiment ON walk_forward_folds(experiment_id);

-- Member-run provenance, parallel to comparison_id.
ALTER TABLE backtest_runs
    ADD COLUMN walk_forward_id UUID REFERENCES walk_forward_experiments(id);

CREATE INDEX idx_runs_walk_forward ON backtest_runs(walk_forward_id)
    WHERE walk_forward_id IS NOT NULL;
