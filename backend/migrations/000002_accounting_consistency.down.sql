ALTER TABLE strategies DROP COLUMN supported_timeframes;
ALTER TABLE backtest_runs
    DROP COLUMN engine_version,
    DROP COLUMN strategy_version,
    DROP COLUMN dataset;
ALTER TABLE backtest_trades
    DROP COLUMN gross_pnl,
    DROP COLUMN entry_commission,
    DROP COLUMN exit_commission;
