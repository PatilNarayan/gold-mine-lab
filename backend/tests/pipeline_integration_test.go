//go:build integration

// Integration test for the Phase 2 pipeline: real Postgres + real CSV data.
// Requires DATABASE_URL and MARKET_DATA_DIR env vars.
//
//	DATABASE_URL=postgres://goldmine:goldmine@localhost:5433/goldmine?sslmode=disable \
//	MARKET_DATA_DIR=../data \
//	go test -tags integration ./...
package tests

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/config"
	"github.com/gold-mine-lab/backend/internal/database"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository/postgres"
	"github.com/gold-mine-lab/backend/internal/service"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/migrations"
)

func pipelineService(t *testing.T) (*service.Service, func()) {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	dataDir := os.Getenv("MARKET_DATA_DIR")
	if dbURL == "" || dataDir == "" {
		t.Skip("DATABASE_URL / MARKET_DATA_DIR unset")
	}
	if err := migrations.Up(dbURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := database.NewPool(context.Background(), config.DBConfig{URL: dbURL, MaxConns: 5, MinConns: 1})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	store := postgres.New(pool.Pool)
	reg := strategy.NewRegistry()
	if err := reg.Register(strategy.NewEMACross()); err != nil {
		t.Fatal(err)
	}
	svc := service.New(service.Repositories{
		Assets: store, Strategies: store, Signals: store,
		Runs: store, Trades: store, Metrics: store,
	}, market.NewCSVProvider(dataDir, nil), reg, slog.Default())
	return svc, pool.Close
}

// Full pipeline: bootstrap → POST-equivalent Execute → persisted detail.
func TestPipelineEndToEnd(t *testing.T) {
	svc, cleanup := pipelineService(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := svc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Assets come from the DB now.
	assets, err := svc.Assets(ctx)
	if err != nil || len(assets) == 0 {
		t.Fatalf("assets: %v (%d)", err, len(assets))
	}

	// Signals were generated from real candles and persisted.
	sigs, err := svc.Signals(ctx, "xauusd", "ema-cross", 5)
	if err != nil || len(sigs) == 0 {
		t.Fatalf("signals: %v (%d)", err, len(sigs))
	}
	if sigs[0].Timestamp.Location().String() != "UTC" {
		t.Fatalf("signal ts not UTC: %v", sigs[0].Timestamp)
	}

	// A fresh backtest through the service persists a real run.
	run, err := svc.Execute(ctx, api.BacktestRequest{
		StrategyID: "ema-cross", AssetID: "xauusd", Timeframe: "4h",
		StartDate: "2024-01-01", EndDate: "2024-12-31",
		InitialCapital: 50000, PositionSize: 5, Commission: 0.02, Slippage: 0.01,
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run status %s", run.Status)
	}

	detail, err := svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Metrics == nil || len(detail.Trades) == 0 || len(detail.Equity) == 0 {
		t.Fatalf("incomplete detail: metrics=%v trades=%d equity=%d",
			detail.Metrics, len(detail.Trades), len(detail.Equity))
	}

	sum, err := svc.DashboardSummary(ctx)
	if err != nil || sum.TotalBacktests == 0 {
		t.Fatalf("summary: %v (%d)", err, sum.TotalBacktests)
	}
}

// Bad requests must produce ValidationError → 400, not a stored failed row
// for input-validation reasons.
func TestExecuteValidation(t *testing.T) {
	svc, cleanup := pipelineService(t)
	defer cleanup()
	ctx := context.Background()

	bad := api.BacktestRequest{
		StrategyID: "ema-cross", AssetID: "xauusd", Timeframe: "1h",
		StartDate: "2025-06-01", EndDate: "2025-01-01", // end < start
		InitialCapital: 10000, PositionSize: 5,
	}
	if _, err := svc.Execute(ctx, bad); err == nil {
		t.Fatal("end<start must fail")
	}
}
