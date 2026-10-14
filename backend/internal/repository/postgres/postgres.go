// Package postgres implements the repository contracts over pgxpool.
// All queries are parameterized; values are never interpolated into SQL.
package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store implements all repository interfaces against one pool.
type Store struct {
	db *pgxpool.Pool
}

// New wraps a pgx pool.
func New(db *pgxpool.Pool) *Store { return &Store{db: db} }
