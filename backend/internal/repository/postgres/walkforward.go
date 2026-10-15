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

// --- walk-forward experiments (Phase 5) --------------------------------------

func (s *Store) CreateExperiment(ctx context.Context, e repository.WalkForwardExperimentRow) (string, error) {
	ids, _ := json.Marshal(e.StrategyIDs)
	const q = `
		INSERT INTO walk_forward_experiments
		    (asset_id, timeframe, range_start, range_end, is_months, oos_months, step_months,
		     initial_capital, position_size, commission, slippage, strategy_ids, status)
		VALUES ($1, $2, $3::date, $4::date, $5, $6, $7, $8, $9, $10, $11, $12, 'pending')
		RETURNING id::text`
	var id string
	err := s.db.QueryRow(ctx, q,
		e.AssetID, e.Timeframe, e.RangeStart, e.RangeEnd, e.IsMonths, e.OosMonths, e.StepMonths,
		e.InitialCapital, e.PositionSize, e.Commission, e.Slippage, ids,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create experiment: %w", err)
	}
	return id, nil
}

func (s *Store) SetExperimentStatus(ctx context.Context, id, status string, errMsg *string) error {
	const q = `UPDATE walk_forward_experiments
		SET status = $2, error = $3, completed_at = CASE WHEN $2 IN ('completed','partial_failed','failed') THEN now() ELSE NULL END
		WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, id, status, errMsg)
	if err != nil {
		return fmt.Errorf("set experiment status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

const experimentCols = `id::text, asset_id, timeframe, range_start, range_end,
	is_months, oos_months, step_months, initial_capital, position_size,
	commission, slippage, strategy_ids, status, error, created_at, completed_at`

func (s *Store) GetExperiment(ctx context.Context, id string) (repository.WalkForwardExperimentRow, error) {
	row := s.db.QueryRow(ctx, `SELECT `+experimentCols+` FROM walk_forward_experiments WHERE id = $1`, id)
	e, err := scanExperiment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, repository.ErrNotFound
	}
	return e, err
}

func (s *Store) ListExperiments(ctx context.Context) ([]repository.WalkForwardExperimentRow, error) {
	rows, err := s.db.Query(ctx, `SELECT `+experimentCols+` FROM walk_forward_experiments ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list experiments: %w", err)
	}
	defer rows.Close()
	out := []repository.WalkForwardExperimentRow{}
	for rows.Next() {
		e, err := scanExperiment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanExperiment(row pgx.Row) (repository.WalkForwardExperimentRow, error) {
	var e repository.WalkForwardExperimentRow
	var ids []byte
	var rs, re time.Time
	err := row.Scan(&e.ID, &e.AssetID, &e.Timeframe, &rs, &re,
		&e.IsMonths, &e.OosMonths, &e.StepMonths, &e.InitialCapital, &e.PositionSize,
		&e.Commission, &e.Slippage, &ids, &e.Status, &e.Error, &e.CreatedAt, &e.CompletedAt)
	if err != nil {
		return e, err
	}
	e.RangeStart = rs.Format("2006-01-02")
	e.RangeEnd = re.Format("2006-01-02")
	if err := json.Unmarshal(ids, &e.StrategyIDs); err != nil {
		return e, fmt.Errorf("decode strategy_ids: %w", err)
	}
	return e, nil
}

// --- folds -------------------------------------------------------------------

func (s *Store) CreateFold(ctx context.Context, f repository.WalkForwardFoldRow) (string, error) {
	const q = `
		INSERT INTO walk_forward_folds
		    (experiment_id, fold_index, strategy_id, is_start, is_end, oos_start, oos_end)
		VALUES ($1, $2, $3, $4::date, $5::date, $6::date, $7::date)
		RETURNING id::text`
	var id string
	err := s.db.QueryRow(ctx, q,
		f.ExperimentID, f.FoldIndex, f.StrategyID, f.IsStart, f.IsEnd, f.OosStart, f.OosEnd,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create fold: %w", err)
	}
	return id, nil
}

func (s *Store) SetFoldResult(ctx context.Context, id, status string, isRunID, oosRunID, errMsg *string) error {
	const q = `UPDATE walk_forward_folds
		SET status = $2, is_run_id = COALESCE($3::uuid, is_run_id), oos_run_id = COALESCE($4::uuid, oos_run_id), error = $5
		WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, id, status, isRunID, oosRunID, errMsg)
	if err != nil {
		return fmt.Errorf("set fold result: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

const foldCols = `id::text, experiment_id::text, fold_index, strategy_id,
	is_start, is_end, oos_start, oos_end, is_run_id::text, oos_run_id::text, status, error`

func (s *Store) ListFolds(ctx context.Context, experimentID string) ([]repository.WalkForwardFoldRow, error) {
	rows, err := s.db.Query(ctx, `SELECT `+foldCols+` FROM walk_forward_folds WHERE experiment_id = $1
		ORDER BY fold_index, strategy_id`, experimentID)
	if err != nil {
		return nil, fmt.Errorf("list folds: %w", err)
	}
	defer rows.Close()
	out := []repository.WalkForwardFoldRow{}
	for rows.Next() {
		var f repository.WalkForwardFoldRow
		var is, ie, os_, oe time.Time
		if err := rows.Scan(&f.ID, &f.ExperimentID, &f.FoldIndex, &f.StrategyID,
			&is, &ie, &os_, &oe, &f.IsRunID, &f.OosRunID, &f.Status, &f.Error); err != nil {
			return nil, err
		}
		f.IsStart = is.Format("2006-01-02")
		f.IsEnd = ie.Format("2006-01-02")
		f.OosStart = os_.Format("2006-01-02")
		f.OosEnd = oe.Format("2006-01-02")
		out = append(out, f)
	}
	return out, rows.Err()
}
