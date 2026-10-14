package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// --- strategies + signals ---------------------------------------------------

func (s *Service) List(ctx context.Context) ([]api.StrategyListItem, error) {
	defs, err := s.repos.Strategies.ListStrategies(ctx)
	if err != nil {
		return nil, err
	}
	runs, err := s.repos.Runs.ListRuns(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]api.StrategyListItem, 0, len(defs))
	for _, d := range defs {
		last, err := s.repos.Signals.LatestSignal(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		perf, err := s.performanceFor(ctx, d.ID, runs)
		if err != nil {
			return nil, err
		}
		out = append(out, api.StrategyListItem{
			Definition:  d,
			LastSignal:  last,
			Performance: perf,
		})
	}
	return out, nil
}

// performanceFor aggregates a strategy's completed runs from its metric rows.
func (s *Service) performanceFor(ctx context.Context, strategyID string, runs []repository.RunRow) (api.StrategyPerformance, error) {
	var perf api.StrategyPerformance
	for _, r := range runs {
		if r.StrategyID != strategyID || r.Status != "completed" {
			continue
		}
		perf.TotalBacktests++
		m, err := s.repos.Metrics.GetMetrics(ctx, r.ID)
		if err != nil {
			return perf, err
		}
		perf.NetPnl += m["net_pnl"]
		perf.WinRate += m["win_rate"]
	}
	if perf.TotalBacktests > 0 {
		perf.WinRate /= float64(perf.TotalBacktests)
	}
	return perf, nil
}

func (s *Service) Get(ctx context.Context, id string) (strategy.Definition, error) {
	d, err := s.repos.Strategies.GetStrategy(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return d, api.ErrNotFound
	}
	return d, err
}

func (s *Service) Signals(ctx context.Context, assetID, strategyID string, limit int) ([]strategy.Signal, error) {
	return s.repos.Signals.ListSignals(ctx, assetID, strategyID, limit)
}

// generateSignals streams the tail of each strategy×asset series through the
// strategy and returns the actionable signals plus the latest bar's signal.
// Persisted by bootstrap; NEUTRAL noise is dropped except the final bar.
func (s *Service) generateSignals(ctx context.Context) ([]strategy.Signal, error) {
	var out []strategy.Signal
	for _, st := range s.registry.List() {
		for _, assetID := range st.SupportedAssets() {
			asset, err := s.repos.Assets.GetAsset(ctx, assetID)
			if err != nil {
				return nil, fmt.Errorf("signals: asset %s: %w", assetID, err)
			}
			candles, err := s.data.Tail(asset, st.Timeframe(), 4000, simulatedNow)
			if err != nil {
				return nil, fmt.Errorf("signals: %s %s: %w", assetID, st.Timeframe(), err)
			}
			streamer, ok := st.(strategy.Streamer)
			if !ok {
				return nil, fmt.Errorf("signals: strategy %s cannot stream", st.ID())
			}
			stream := streamer.NewStream()
			var last strategy.Signal
			for _, c := range candles {
				last = stream.Next(c)
				if last.Action == strategy.ActionBuy || last.Action == strategy.ActionSell {
					last.AssetID = assetID
					out = append(out, last)
				}
			}
			// Always keep the most recent bar so the UI has a "current" state.
			if len(candles) > 0 && (len(out) == 0 || out[len(out)-1].Timestamp != last.Timestamp) {
				last.AssetID = assetID
				out = append(out, last)
			}
		}
	}
	return out, nil
}
