package postgres

import (
	"context"

	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/jackc/pgx/v5"
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

func (s *Store) CreateRevision(ctx context.Context, graphID string, definition []byte, hash string) (repository.GraphRevisionRow, error) {
	var r repository.GraphRevisionRow
	// COALESCE+1 gives the next immutable revision; concurrent creates for the
	// same graph could collide on the PK — acceptable for the MVP volume.
	err := s.db.QueryRow(ctx,
		`INSERT INTO strategy_graph_revisions (graph_id, revision, definition, definition_hash)
		 VALUES ($1, COALESCE((SELECT max(revision) FROM strategy_graph_revisions WHERE graph_id=$1), 0) + 1, $2, $3)
		 RETURNING graph_id, revision, definition, definition_hash, created_at`,
		graphID, definition, hash).
		Scan(&r.GraphID, &r.Revision, &r.Definition, &r.DefinitionHash, &r.CreatedAt)
	return r, err
}
