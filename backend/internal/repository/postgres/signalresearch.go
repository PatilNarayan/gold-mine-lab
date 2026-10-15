package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gold-mine-lab/backend/internal/repository"
)

func (s *Store) CreateSignalResearch(ctx context.Context, request []byte) (string, error) {
	const q = `INSERT INTO signal_research (request, status) VALUES ($1, 'pending') RETURNING id::text`
	var id string
	if err := s.db.QueryRow(ctx, q, request).Scan(&id); err != nil {
		return "", fmt.Errorf("create signal research: %w", err)
	}
	return id, nil
}

func (s *Store) SetSignalResearchResult(ctx context.Context, id, status string, result []byte, errMsg *string) error {
	const q = `UPDATE signal_research
		SET status = $2, result = $3, error = $4,
		    completed_at = CASE WHEN $2 IN ('completed','failed') THEN now() ELSE NULL END
		WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, id, status, result, errMsg)
	if err != nil {
		return fmt.Errorf("set signal research result: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

const signalResearchCols = `id::text, status, request, result, error, created_at, completed_at`

func (s *Store) GetSignalResearch(ctx context.Context, id string) (repository.SignalResearchRow, error) {
	row := s.db.QueryRow(ctx, `SELECT `+signalResearchCols+` FROM signal_research WHERE id = $1`, id)
	c, err := scanSignalResearch(row)
	if err == nil {
		return c, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return c, repository.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22P02" { // invalid uuid → not found
		return c, repository.ErrNotFound
	}
	return c, err
}

func (s *Store) ListSignalResearch(ctx context.Context) ([]repository.SignalResearchRow, error) {
	rows, err := s.db.Query(ctx, `SELECT `+signalResearchCols+` FROM signal_research ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list signal research: %w", err)
	}
	defer rows.Close()
	var out []repository.SignalResearchRow
	for rows.Next() {
		c, err := scanSignalResearch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type signalResearchScanner interface {
	Scan(dest ...any) error
}

func scanSignalResearch(row signalResearchScanner) (repository.SignalResearchRow, error) {
	var c repository.SignalResearchRow
	var completedAt *time.Time
	err := row.Scan(&c.ID, &c.Status, &c.Request, &c.Result, &c.Error, &c.CreatedAt, &completedAt)
	c.CompletedAt = completedAt
	return c, err
}
