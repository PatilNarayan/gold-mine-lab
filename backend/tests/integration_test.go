//go:build integration

// Package tests holds integration tests that need real infrastructure.
// Run with: go test -tags=integration ./tests/  (requires DATABASE_URL and
// a reachable Postgres — see docker-compose.yml).
package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/config"
	"github.com/gold-mine-lab/backend/internal/database"
)

func TestPoolPingRealDB(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set — skipping real DB integration test")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := database.NewPool(ctx, cfg.DB)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
