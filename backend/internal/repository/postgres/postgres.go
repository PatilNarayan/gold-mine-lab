// Package postgres implements the repository contracts over pgxpool.
// All queries are parameterized; values are never interpolated into SQL.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store implements all repository interfaces against one pool.
type Store struct {
	db *pgxpool.Pool
}

// dbtx is satisfied by *pgxpool.Pool and pgx.Tx so helpers can run either
// standalone or inside a transaction.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// New wraps a pgx pool.
func New(db *pgxpool.Pool) *Store { return &Store{db: db} }
