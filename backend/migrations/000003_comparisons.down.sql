DROP INDEX IF EXISTS idx_runs_comparison;
ALTER TABLE backtest_runs
    DROP COLUMN comparison_id,
    DROP COLUMN sample;
DROP TABLE IF EXISTS comparisons;
