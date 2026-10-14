package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gold-mine-lab/backend/internal/repository"
)

func (s *Store) CreateRun(ctx context.Context, r repository.RunRow) (string, error) {
	params, _ := json.Marshal(r.Parameters)
	const q = `
		INSERT INTO backtest_runs
		    (strategy_id, asset_id, timeframe, start_date, end_date,
		     initial_capital, position_size, commission, slippage, parameters, status)
		VALUES ($1, $2, $3, $4::date, $5::date, $6, $7, $8, $9, $10, 'queued')
		RETURNING id::text`
	var id string
	err := s.db.QueryRow(ctx, q,
		r.StrategyID, r.AssetID, r.Timeframe, r.StartDate, r.EndDate,
		r.InitialCapital, r.PositionSize, r.Commission, r.Slippage, params,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create run: %w", err)
	}
	return id, nil
}

func (s *Store) MarkRunRunning(ctx context.Context, runID string) error {
	const q = `UPDATE backtest_runs SET status = 'running' WHERE id = $1 AND status = 'queued'`
	tag, err := s.db.Exec(ctx, q, runID)
	if err != nil {
		return fmt.Errorf("mark run running: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// CompleteRun writes trades + metrics + equity curve and marks the run
// completed in a single transaction — a run is never half-persisted.
func (s *Store) CompleteRun(ctx context.Context, runID string, trades []repository.TradeRow, metrics []repository.MetricValue, equity []repository.EquityPointRow) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if len(trades) > 0 {
		rows := make([][]any, 0, len(trades))
		for _, t := range trades {
			rows = append(rows, []any{
				runID, t.StrategyID, t.AssetID, t.Side,
				t.EntryTime, t.EntryPrice, t.ExitTime, t.ExitPrice,
				t.Quantity, t.Pnl, t.Status, t.Result,
			})
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"backtest_trades"},
			[]string{"run_id", "strategy_id", "asset_id", "side", "entry_time", "entry_price",
				"exit_time", "exit_price", "quantity", "pnl", "status", "result"},
			pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("insert trades: %w", err)
		}
	}

	batch := &pgx.Batch{}
	for _, m := range metrics {
		batch.Queue(`
			INSERT INTO backtest_metrics (run_id, metric_name, metric_value)
			VALUES ($1, $2, $3)
			ON CONFLICT (run_id, metric_name) DO UPDATE SET metric_value = EXCLUDED.metric_value`,
			runID, m.Name, m.Value)
	}
	res := tx.SendBatch(ctx, batch)
	if err := res.Close(); err != nil {
		return fmt.Errorf("insert metrics: %w", err)
	}

	curve, _ := json.Marshal(equity)
	const q = `
		UPDATE backtest_runs
		SET status = 'completed', equity_curve = $2, completed_at = now()
		WHERE id = $1`
	tag, err := tx.Exec(ctx, q, runID, curve)
	if err != nil {
		return fmt.Errorf("complete run: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) FailRun(ctx context.Context, runID, message string) error {
	const q = `UPDATE backtest_runs SET status = 'failed', error = $2, completed_at = now() WHERE id = $1`
	if _, err := s.db.Exec(ctx, q, runID, message); err != nil {
		return fmt.Errorf("fail run: %w", err)
	}
	return nil
}

const runCols = `id::text, strategy_id, asset_id, timeframe, start_date, end_date,
	initial_capital, position_size, commission, slippage, parameters,
	status, equity_curve, error, created_at, completed_at`

func (s *Store) GetRun(ctx context.Context, runID string) (repository.RunRow, error) {
	row := s.db.QueryRow(ctx, `SELECT `+runCols+` FROM backtest_runs WHERE id = $1`, runID)
	r, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, repository.ErrNotFound
	}
	return r, err
}

func (s *Store) ListRuns(ctx context.Context, strategyID string) ([]repository.RunRow, error) {
	q := `SELECT ` + runCols + ` FROM backtest_runs`
	args := []any{}
	if strategyID != "" {
		args = append(args, strategyID)
		q += ` WHERE strategy_id = $1`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	out := []repository.RunRow{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) LatestCompletedRunID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`SELECT id::text FROM backtest_runs WHERE status = 'completed' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func scanRun(row pgx.Row) (repository.RunRow, error) {
	var r repository.RunRow
	var params, curve []byte
	var startDate, endDate time.Time // DATE columns scan to midnight time.Time
	err := row.Scan(&r.ID, &r.StrategyID, &r.AssetID, &r.Timeframe,
		&startDate, &endDate,
		&r.InitialCapital, &r.PositionSize, &r.Commission, &r.Slippage,
		&params, &r.Status, &curve, &r.Error, &r.CreatedAt, &r.CompletedAt)
	if err != nil {
		return r, err
	}
	r.StartDate = startDate.Format("2006-01-02")
	r.EndDate = endDate.Format("2006-01-02")
	if len(params) > 0 {
		_ = json.Unmarshal(params, &r.Parameters)
	}
	if len(curve) > 0 {
		_ = json.Unmarshal(curve, &r.EquityCurve)
	}
	return r, nil
}
