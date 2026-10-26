package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// SeedAsset is the single asset Phase 2 focuses on; more arrive with data.
var SeedAsset = market.Asset{
	ID: "xauusd", Symbol: "XAUUSD", Name: "Gold Spot",
	AssetClass: "metal", QuoteCurrency: "USD",
}

// Bootstrap seeds the database and runs the pipeline once so the UI has real
// data: upsert asset + strategy definitions, generate signals from real
// candles, and (on an empty DB) execute seed backtests through the real
// engine. Idempotent — safe on every startup.
func (s *Service) Bootstrap(ctx context.Context) error {
	if err := s.repos.Assets.UpsertAsset(ctx, SeedAsset); err != nil {
		return fmt.Errorf("seed asset: %w", err)
	}
	for _, st := range s.registry.List() {
		d := describe(st)
		if err := s.repos.Strategies.UpsertStrategy(ctx, d); err != nil {
			return fmt.Errorf("seed strategy %s: %w", d.ID, err)
		}
	}
	s.log.Info("bootstrap: asset and strategies seeded")

	// Pre-warm the provider-synced candle store for the research timeframes
	// so the first chart/backtest doesn't pay the cold-sync cost. Failure is
	// non-fatal: the store serves whatever synced before and retries lazily.
	if dbp, ok := s.data.(*market.DBProvider); ok {
		floor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		for _, tf := range []market.Timeframe{market.Timeframe1d, market.Timeframe4h, market.Timeframe1h, market.Timeframe15m} {
			if err := dbp.Ensure(ctx, SeedAsset, tf, floor, time.Now().UTC()); err != nil {
				s.log.Warn("bootstrap: candle sync incomplete — will retry on demand",
					"timeframe", tf, "err", err)
			}
		}
	}

	sigs, err := s.generateSignals(ctx)
	if err != nil {
		return fmt.Errorf("generate signals: %w", err)
	}
	n, err := s.repos.Signals.InsertSignals(ctx, sigs)
	if err != nil {
		return fmt.Errorf("persist signals: %w", err)
	}
	s.log.Info("bootstrap: signals generated", "computed", len(sigs), "inserted", n)

	runs, err := s.repos.Runs.ListRuns(ctx, "")
	if err != nil {
		return fmt.Errorf("check existing runs: %w", err)
	}
	if len(runs) == 0 {
		s.log.Info("bootstrap: empty database — seeding backtests")
		for _, req := range seedRequests() {
			if _, err := s.Execute(ctx, req); err != nil {
				// Seeding is convenience, not a startup gate — an unavailable
				// provider must not prevent the app from serving.
				s.log.Warn("bootstrap: seed backtest failed", "strategy", req.StrategyID, "err", err)
			}
		}
	}
	return nil
}

// seedRequests are real engine runs over real data — a spread of windows.
func seedRequests() []api.BacktestRequest {
	return []api.BacktestRequest{
		{StrategyID: "ema-cross", AssetID: "xauusd", Timeframe: "1h",
			StartDate: "2024-01-01", EndDate: "2025-09-01",
			InitialCapital: 100000, PositionSize: 5, Commission: 0.02, Slippage: 0.01,
			Parameters: map[string]any{}},
		{StrategyID: "ema-cross", AssetID: "xauusd", Timeframe: "4h",
			StartDate: "2023-01-01", EndDate: "2025-09-01",
			InitialCapital: 100000, PositionSize: 5, Commission: 0.02, Slippage: 0.01,
			Parameters: map[string]any{}},
		{StrategyID: "ema-cross", AssetID: "xauusd", Timeframe: "1d",
			StartDate: "2020-01-01", EndDate: "2025-09-01",
			InitialCapital: 100000, PositionSize: 10, Commission: 0.02, Slippage: 0.01,
			Parameters: map[string]any{}},
	}
}

// describe extracts the serializable Definition from a registered strategy.
func describe(st strategy.Strategy) strategy.Definition {
	if d, ok := st.(interface{ Describe() strategy.Definition }); ok {
		return d.Describe()
	}
	return strategy.Definition{
		ID: st.ID(), Name: st.Name(), Version: st.Version(),
		SupportedAssets: st.SupportedAssets(), Timeframe: st.Timeframe(),
		Parameters: st.Parameters(), Status: "active",
	}
}
