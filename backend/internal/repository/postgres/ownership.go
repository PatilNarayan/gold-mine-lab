package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ShadowOwnership is a PostgreSQL advisory-lock lease held on ONE dedicated
// connection checked out of the pool — the connection is never returned
// while the lease is held, so the session-scoped lock cannot silently
// migrate to another backend or be released by pool churn. A watchdog pings
// the held connection: losing it means losing the lock (the session died),
// which Lost() reports so the runtime can degrade instead of risking a
// second live writer.
type ShadowOwnership struct {
	conn *pgxpool.Conn
	key  int64
	lost chan struct{}
	done chan struct{}

	lostOnce sync.Once
	doneOnce sync.Once
}

// AcquireShadowOwnership takes pg_try_advisory_lock(key) on a dedicated
// connection. A non-nil error means another owner holds the lease or the
// database is unreachable — the caller must NOT serve an active runtime.
func (s *Store) AcquireShadowOwnership(ctx context.Context, key int64) (*ShadowOwnership, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire ownership connection: %w", err)
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok); err != nil {
		conn.Release()
		return nil, fmt.Errorf("advisory lock: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, fmt.Errorf("shadow runtime ownership refused — key %d already held", key)
	}
	o := &ShadowOwnership{
		conn: conn, key: key,
		lost: make(chan struct{}), done: make(chan struct{}),
	}
	go o.watch()
	return o, nil
}

// watch pings the held connection — its death is the lease's death.
func (o *ShadowOwnership) watch() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-o.done:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := o.conn.Ping(ctx)
			cancel()
			if err != nil {
				o.lostOnce.Do(func() { close(o.lost) })
				return
			}
		}
	}
}

// Lost closes when the lease is gone underneath us (connection death).
func (o *ShadowOwnership) Lost() <-chan struct{} { return o.lost }

// Release unlocks and returns the connection to the pool — idempotent;
// the watchdog stops first so it cannot report a loss we caused ourselves.
func (o *ShadowOwnership) Release(ctx context.Context) error {
	o.doneOnce.Do(func() { close(o.done) })
	defer o.conn.Release()
	if _, err := o.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, o.key); err != nil {
		return fmt.Errorf("advisory unlock: %w", err)
	}
	return nil
}
