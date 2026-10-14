-- Phase 2.1: accounting consistency + experiment provenance.
-- Forward-only; existing rows are preserved and marked as produced by the
-- legacy engine (they cannot be reconstructed — engine 2.1 stores net P&L
-- and gross/commissions separately, which old rows did not record).

-- Per-trade cost decomposition. Legacy rows keep pnl as stored (gross) with
-- NULL in the new columns; new runs record pnl = NET and all components.
ALTER TABLE backtest_trades
    ADD COLUMN gross_pnl         DOUBLE PRECISION,
    ADD COLUMN entry_commission  DOUBLE PRECISION,
    ADD COLUMN exit_commission   DOUBLE PRECISION;

-- Run provenance: which engine/strategy versions and dataset produced it.
ALTER TABLE backtest_runs
    ADD COLUMN engine_version    TEXT,
    ADD COLUMN strategy_version  TEXT,
    ADD COLUMN dataset           TEXT;

-- Existing rows predate engine 2.1 — mark them legacy so consumers know
-- their trade P&Ls are gross and not comparable field-by-field with new runs.
UPDATE backtest_runs
SET engine_version = 'legacy',
    strategy_version = COALESCE(strategy_version, 'legacy'),
    dataset = COALESCE(dataset, 'unknown')
WHERE engine_version IS NULL;

-- Per-run timeframe is now a supported set, not the strategy's fixed choice.
ALTER TABLE strategies
    ADD COLUMN supported_timeframes JSONB NOT NULL DEFAULT '[]'::jsonb;
