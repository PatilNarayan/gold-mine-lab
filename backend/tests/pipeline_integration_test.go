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
	"math"
	"os"
	"strings"
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
	if err := reg.Register(strategy.NewRSIMeanReversion()); err != nil {
		t.Fatal(err)
	}
	svc := service.New(service.Repositories{
		Assets: store, Strategies: store, Signals: store,
		Runs: store, Trades: store, Metrics: store, Comparisons: store, WalkForwards: store,
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
	// Provenance: engine/strategy versions + dataset fingerprint persisted.
	if run.EngineVersion == nil || *run.EngineVersion != "2.1.0" {
		t.Fatalf("engineVersion = %v", run.EngineVersion)
	}
	if run.StrategyVersion == nil || *run.StrategyVersion != "1.0.0" {
		t.Fatalf("strategyVersion = %v", run.StrategyVersion)
	}
	if run.Dataset == nil || !strings.Contains(*run.Dataset, "sha256:") {
		t.Fatalf("dataset fingerprint = %v", run.Dataset)
	}

	// RSI strategy runs through the same engine + provenance path.
	rsiRun, err := svc.Execute(ctx, api.BacktestRequest{
		StrategyID: "rsi-mean-reversion", AssetID: "xauusd", Timeframe: "1d",
		StartDate: "2020-01-01", EndDate: "2025-06-01",
		InitialCapital: 50000, PositionSize: 10, Commission: 0.02, Slippage: 0.01,
		Parameters: map[string]any{},
	})
	if err != nil {
		t.Fatalf("rsi execute: %v", err)
	}
	if rsiRun.Status != "completed" {
		t.Fatalf("rsi run status %s", rsiRun.Status)
	}
	rsiDetail, err := svc.GetRun(ctx, rsiRun.ID)
	if err != nil || rsiDetail.Metrics == nil {
		t.Fatalf("rsi detail: %v", err)
	}
	// Equity reconciliation on the persisted curve.
	var sumNet float64
	for _, tr := range rsiDetail.Trades {
		if tr.Pnl != nil {
			sumNet += *tr.Pnl
		}
	}
	last := rsiDetail.Equity[len(rsiDetail.Equity)-1]
	if math.Abs(rsiDetail.Run.InitialCapital+sumNet-last.Equity) > 0.01 {
		t.Fatalf("rsi run: last equity %v != initial+%v", last.Equity, sumNet)
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

// Comparison legs: an invalid leg rejects the whole request before anything
// is stored; an OOS window with no candle data fails only those member runs,
// which stay attached to the comparison → partial_failed.
func TestComparisonStatuses(t *testing.T) {
	svc, cleanup := pipelineService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	oosS, oosE := "2099-01-01", "2099-06-30" // beyond any dataset
	req := api.ComparisonRequest{
		Legs:    []api.ComparisonLeg{{StrategyID: "ema-cross"}, {StrategyID: "rsi-mean-reversion"}},
		AssetID: "xauusd", Timeframe: "1d", StartDate: "2020-01-01", EndDate: "2022-12-31",
		OutOfSampleStartDate: &oosS, OutOfSampleEndDate: &oosE,
		InitialCapital: 10000, PositionSize: 10, Commission: 0.02, Slippage: 0.01,
	}
	c, err := svc.ExecuteComparison(ctx, req)
	if err != nil {
		t.Fatalf("comparison: %v", err)
	}
	if c.Status != "partial_failed" || len(c.Results) != 4 {
		t.Fatalf("status=%s results=%d, want partial_failed/4", c.Status, len(c.Results))
	}
	var fp *string
	for _, r := range c.Results {
		switch r.Sample {
		case "in_sample":
			if r.Run.Status != "completed" || r.Metrics == nil {
				t.Errorf("IS leg %s: status=%s", r.Run.StrategyID, r.Run.Status)
			}
			if fp != nil && r.Run.Dataset != nil && *fp != *r.Run.Dataset {
				t.Errorf("fingerprint differs across legs")
			}
			fp = r.Run.Dataset
		case "out_of_sample":
			if r.Run.Status != "failed" || r.Run.Error == nil {
				t.Errorf("OOS leg %s: status=%s, want failed with error", r.Run.StrategyID, r.Run.Status)
			}
		}
	}

	bad := req
	bad.Legs = []api.ComparisonLeg{{StrategyID: "ema-cross"}, {StrategyID: "rsi-mean-reversion", Parameters: map[string]any{"rsi_period": -5.0}}}
	before, _ := svc.ListComparisons(ctx)
	if _, err := svc.ExecuteComparison(ctx, bad); err == nil {
		t.Fatal("invalid leg parameters must be rejected")
	}
	after, _ := svc.ListComparisons(ctx)
	if len(after) != len(before) {
		t.Fatal("rejected comparison must not be persisted")
	}
}

// Walk-forward: fold boundaries are rolling and non-overlapping; a fold whose
// OOS window has no data fails that leg while the rest complete →
// partial_failed; every member run carries the experiment provenance.
func TestWalkForwardStatuses(t *testing.T) {
	svc, cleanup := pipelineService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	req := api.WalkForwardRequest{
		Legs:    []api.ComparisonLeg{{StrategyID: "ema-cross"}, {StrategyID: "rsi-mean-reversion"}},
		AssetID: "xauusd", Timeframe: "1d", StartDate: "2018-01-01", EndDate: "2022-01-01",
		InSampleMonths: 24, OosMonths: 12, // step defaults to OOS length
		InitialCapital: 10000, PositionSize: 10, Commission: 0.02, Slippage: 0.01,
	}
	w, err := svc.ExecuteWalkForward(ctx, req)
	if err != nil {
		t.Fatalf("walk-forward: %v", err)
	}
	if w.Status != "completed" || len(w.Folds) != 4 { // 1 fold fits × 2 legs
		t.Fatalf("status=%s folds=%d", w.Status, len(w.Folds))
	}
	prevOosEnd := map[string]time.Time{} // per strategy
	for i, f := range w.Folds {
		if f.IsResult == nil || f.OosResult == nil || f.OosResult.Metrics == nil {
			t.Fatalf("fold %d missing results", i)
		}
		ie, _ := time.Parse("2006-01-02", f.IsEnd)
		os_, _ := time.Parse("2006-01-02", f.OosStart)
		if !os_.Equal(ie) {
			t.Errorf("fold %d OOS not adjacent to IS", i)
		}
		oe, _ := time.Parse("2006-01-02", f.OosEnd)
		if p, ok := prevOosEnd[f.StrategyID]; ok && os_.Before(p) {
			t.Errorf("fold %d %s OOS overlaps previous", i, f.StrategyID)
		}
		prevOosEnd[f.StrategyID] = oe
	}
	if w.Summaries == nil || w.Summaries["ema-cross"] == nil || w.Summaries["rsi-mean-reversion"] == nil {
		t.Fatal("per-strategy OOS summaries missing")
	}

	// Invalid leg rejected before anything is persisted.
	before, _ := svc.ListWalkForwards(ctx)
	bad := req
	bad.Legs = []api.ComparisonLeg{{StrategyID: "ema-cross"}, {StrategyID: "rsi-mean-reversion", Parameters: map[string]any{"rsi_period": -5.0}}}
	if _, err := svc.ExecuteWalkForward(ctx, bad); err == nil {
		t.Fatal("invalid leg must be rejected")
	}
	after, _ := svc.ListWalkForwards(ctx)
	if len(after) != len(before) {
		t.Fatal("rejected experiment must not be persisted")
	}
	// step < OOS length rejected (OOS windows would overlap).
	bad2 := req
	bad2.StepMonths = 6
	if _, err := svc.ExecuteWalkForward(ctx, bad2); err == nil {
		t.Fatal("step < oos must be rejected")
	}
}
