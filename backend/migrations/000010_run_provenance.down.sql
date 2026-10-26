ALTER TABLE backtest_runs
    DROP COLUMN IF EXISTS data_provider,
    DROP COLUMN IF EXISTS data_candles,
    DROP COLUMN IF EXISTS data_first_bar,
    DROP COLUMN IF EXISTS data_last_bar;
