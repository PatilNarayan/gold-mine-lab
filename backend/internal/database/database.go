// Package database owns the PostgreSQL connection pool. Phase 1 provides only
// connectivity and a health check; schema migrations arrive in a later phase.
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gold-mine-lab/backend/internal/config"
)

// pingTimeout bounds health checks so a hung database cannot stall requests.
const pingTimeout = 2 * time.Second

// Pool wraps *pgxpool.Pool with project-level helpers.
type Pool struct {
	*pgxpool.Pool
}

// NewPool builds a pgx pool from config. The pool is lazy: connections are
// established on first use, so startup stays cheap and Ping reports real
// reachability.
func NewPool(ctx context.Context, cfg config.DBConfig) (*Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	return &Pool{Pool: pool}, nil
}

// Ping checks database reachability with its own short timeout, independent
// of the caller's deadline.
func (p *Pool) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := p.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping: %w", err)
	}
	return nil
}
