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

func (s *Store) CreateComparison(ctx context.Context, c repository.ComparisonRow) (string, error) {
	ids, _ := json.Marshal(c.StrategyIDs)
	const q = `
		INSERT INTO comparisons
		    (asset_id, timeframe, start_date, end_date, oos_start_date, oos_end_date,
		     initial_capital, position_size, commission, slippage, strategy_ids, status)
		VALUES ($1, $2, $3::date, $4::date, $5::date, $6::date, $7, $8, $9, $10, $11, 'pending')
		RETURNING id::text`
	var id string
	err := s.db.QueryRow(ctx, q,
		c.AssetID, c.Timeframe, c.StartDate, c.EndDate, c.OosStartDate, c.OosEndDate,
		c.InitialCapital, c.PositionSize, c.Commission, c.Slippage, ids,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create comparison: %w", err)
	}
	return id, nil
}

func (s *Store) SetComparisonStatus(ctx context.Context, id, status string, errMsg *string) error {
	const q = `UPDATE comparisons
		SET status = $2, error = $3, completed_at = CASE WHEN $2 IN ('completed','partial_failed','failed') THEN now() ELSE NULL END
		WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, id, status, errMsg)
	if err != nil {
		return fmt.Errorf("set comparison status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

const comparisonCols = `id::text, asset_id, timeframe, start_date, end_date,
	oos_start_date, oos_end_date, initial_capital, position_size,
	commission, slippage, strategy_ids, status, error, created_at, completed_at`

func (s *Store) GetComparison(ctx context.Context, id string) (repository.ComparisonRow, error) {
	row := s.db.QueryRow(ctx, `SELECT `+comparisonCols+` FROM comparisons WHERE id = $1`, id)
	c, err := scanComparison(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, repository.ErrNotFound
	}
	return c, err
}

func (s *Store) ListComparisons(ctx context.Context) ([]repository.ComparisonRow, error) {
	rows, err := s.db.Query(ctx, `SELECT `+comparisonCols+` FROM comparisons ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list comparisons: %w", err)
	}
	defer rows.Close()
	out := []repository.ComparisonRow{}
	for rows.Next() {
		c, err := scanComparison(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanComparison(row pgx.Row) (repository.ComparisonRow, error) {
	var c repository.ComparisonRow
	var ids []byte
	var is, ie, os_, oe *time.Time
	err := row.Scan(&c.ID, &c.AssetID, &c.Timeframe, &is, &ie, &os_, &oe,
		&c.InitialCapital, &c.PositionSize, &c.Commission, &c.Slippage,
		&ids, &c.Status, &c.Error, &c.CreatedAt, &c.CompletedAt)
	if err != nil {
		return c, err
	}
	date := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		s := t.Format("2006-01-02")
		return &s
	}
	c.StartDate = *date(is)
	c.EndDate = *date(ie)
	c.OosStartDate = date(os_)
	c.OosEndDate = date(oe)
	if err := json.Unmarshal(ids, &c.StrategyIDs); err != nil {
		return c, fmt.Errorf("decode strategy_ids: %w", err)
	}
	return c, nil
}
