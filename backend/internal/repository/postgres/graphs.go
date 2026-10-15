package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) ListGraphs(ctx context.Context) ([]repository.StrategyGraphRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT g.id, g.name, g.description, g.status, g.created_at,
		        (SELECT count(*) FROM strategy_graph_revisions r WHERE r.graph_id = g.id)
		 FROM strategy_graphs g ORDER BY g.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repository.StrategyGraphRow
	for rows.Next() {
		var r repository.StrategyGraphRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.Status, &r.CreatedAt, &r.Revisions); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateGraph(ctx context.Context, name, description string) (repository.StrategyGraphRow, error) {
	var r repository.StrategyGraphRow
	err := s.db.QueryRow(ctx,
		`INSERT INTO strategy_graphs (name, description) VALUES ($1,$2)
		 RETURNING id, name, description, status, created_at`,
		name, description).Scan(&r.ID, &r.Name, &r.Description, &r.Status, &r.CreatedAt)
	if err != nil {
		return r, err
	}
	return r, nil
}

func (s *Store) GetGraph(ctx context.Context, id string) (repository.StrategyGraphRow, error) {
	var r repository.StrategyGraphRow
	err := s.db.QueryRow(ctx,
		`SELECT g.id, g.name, g.description, g.status, g.created_at,
		        (SELECT count(*) FROM strategy_graph_revisions r WHERE r.graph_id = g.id)
		 FROM strategy_graphs g WHERE g.id = $1`, id).
		Scan(&r.ID, &r.Name, &r.Description, &r.Status, &r.CreatedAt, &r.Revisions)
	if err == pgx.ErrNoRows {
		return r, repository.ErrNotFound
	}
	return r, err
}

func (s *Store) ListRevisions(ctx context.Context, graphID string) ([]repository.GraphRevisionRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT graph_id, revision, definition, definition_hash, created_at
		 FROM strategy_graph_revisions WHERE graph_id = $1 ORDER BY revision`, graphID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repository.GraphRevisionRow
	for rows.Next() {
		var r repository.GraphRevisionRow
		if err := rows.Scan(&r.GraphID, &r.Revision, &r.Definition, &r.DefinitionHash, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRevision(ctx context.Context, graphID string, revision int) (repository.GraphRevisionRow, error) {
	var r repository.GraphRevisionRow
	err := s.db.QueryRow(ctx,
		`SELECT graph_id, revision, definition, definition_hash, created_at
		 FROM strategy_graph_revisions WHERE graph_id = $1 AND revision = $2`, graphID, revision).
		Scan(&r.GraphID, &r.Revision, &r.Definition, &r.DefinitionHash, &r.CreatedAt)
	if err == pgx.ErrNoRows {
		return r, repository.ErrNotFound
	}
	return r, err
}

// CreateRevisionWithStrategy inserts the next immutable revision AND its
// materialized strategies row in one transaction: a persisted revision can
// therefore never lack the strategy identity runs depend on. stratFor builds
// the strategy Definition for the revision number assigned inside the tx.
// Concurrent creates may collide on (graph_id, revision); each attempt takes
// max+1 inside its own tx, so a short retry resolves the loser honestly.
func (s *Store) CreateRevisionWithStrategy(ctx context.Context, graphID string, definition []byte, hash string, stratFor func(revision int) (strategy.Definition, error)) (repository.GraphRevisionRow, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		r, err := s.createRevisionTx(ctx, graphID, definition, hash, stratFor)
		if err == nil {
			return r, nil
		}
		lastErr = err
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			return r, err
		}
	}
	return repository.GraphRevisionRow{}, fmt.Errorf("create revision: %w", lastErr)
}

func (s *Store) createRevisionTx(ctx context.Context, graphID string, definition []byte, hash string, stratFor func(revision int) (strategy.Definition, error)) (repository.GraphRevisionRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return repository.GraphRevisionRow{}, err
	}
	defer tx.Rollback(ctx)
	var r repository.GraphRevisionRow
	err = tx.QueryRow(ctx,
		`INSERT INTO strategy_graph_revisions (graph_id, revision, definition, definition_hash)
		 VALUES ($1, COALESCE((SELECT max(revision) FROM strategy_graph_revisions WHERE graph_id=$1), 0) + 1, $2, $3)
		 RETURNING graph_id, revision, definition, definition_hash, created_at`,
		graphID, definition, hash).
		Scan(&r.GraphID, &r.Revision, &r.Definition, &r.DefinitionHash, &r.CreatedAt)
	if err != nil {
		return r, err
	}
	def, err := stratFor(r.Revision)
	if err != nil {
		return r, fmt.Errorf("materialize graph strategy: %w", err)
	}
	if err := upsertStrategy(ctx, tx, def); err != nil {
		return r, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r, err
	}
	return r, nil
}
