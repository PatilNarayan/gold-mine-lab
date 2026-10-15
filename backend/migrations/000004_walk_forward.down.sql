DROP INDEX IF EXISTS idx_runs_walk_forward;
ALTER TABLE backtest_runs DROP COLUMN walk_forward_id;
DROP TABLE IF EXISTS walk_forward_folds;
DROP TABLE IF EXISTS walk_forward_experiments;
