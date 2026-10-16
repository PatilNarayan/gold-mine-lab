// Package service implements the api.*Service contracts over real components:
// PostgreSQL repositories, the CSV market-data provider, the strategy
// registry, and the backtest engine. Handlers stay thin; this is where the
// research pipeline is assembled.
//
// Simulated "now": market endpoints truncate data at 2025-10-14 (one year
// back for testing). Backtests run on their explicit date window.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/backtest"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/internal/strategy/indicators"
)

// marketTailAsOf is the zero time — Tail treats it as "the dataset's actual
// end" so the market views report the real latest candle, never a simulated
// "now". Historical data only; there is no live feed in V1.
var marketTailAsOf time.Time

// serveCaps bounds candles returned per timeframe by the market endpoints.
var serveCaps = map[market.Timeframe]int{
	market.Timeframe1m:  800,
	market.Timeframe5m:  800,
	market.Timeframe15m: 800,
	market.Timeframe1h:  1500,
	market.Timeframe4h:  1500,
	market.Timeframe1d:  50000,
}

// Repositories bundles the repository contracts the service consumes.
type Repositories struct {
	Assets         repository.AssetRepository
	Strategies     repository.StrategyRepository
	Signals        repository.SignalRepository
	Runs           repository.RunRepository
	Trades         repository.TradeRepository
	Metrics        repository.MetricRepository
	Comparisons    repository.ComparisonRepository
	WalkForwards   repository.WalkForwardRepository
	StrategyGraphs repository.StrategyGraphRepository
	SignalResearch repository.SignalResearchRepository
	Studies        repository.ResearchStudyRepository
}

// Service implements api.MarketService/StrategyService/BacktestService/
// TradeService over the real pipeline.
type Service struct {
	repos    Repositories
	data     *market.CSVProvider
	registry strategy.Registry
	engine   *backtest.Engine
	log      *slog.Logger
}

// New wires the service. dataDir may be "" only when market endpoints should
// 503 — callers normally pass the configured data directory.
func New(repos Repositories, data *market.CSVProvider, reg strategy.Registry, log *slog.Logger) *Service {
	return &Service{repos: repos, data: data, registry: reg, engine: backtest.NewEngine(), log: log}
}

// --- market ---------------------------------------------------------------

func (s *Service) Assets(ctx context.Context) ([]market.Asset, error) {
	return s.repos.Assets.ListAssets(ctx)
}

func (s *Service) Candles(ctx context.Context, assetID string, tf market.Timeframe) (api.CandleSeries, error) {
	asset, err := s.repos.Assets.GetAsset(ctx, assetID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return api.CandleSeries{}, api.ErrNotFound
		}
		return api.CandleSeries{}, err
	}
	candles, err := s.data.Tail(asset, tf, serveCaps[tf], marketTailAsOf)
	if err != nil {
		return api.CandleSeries{}, fmt.Errorf("%w: %v", api.ErrNotFound, err)
	}
	return seriesFromCandles(assetID, tf, candles), nil
}

func (s *Service) MarketSummary(ctx context.Context, assetID string, tf market.Timeframe) (api.MarketSummary, error) {
	asset, err := s.repos.Assets.GetAsset(ctx, assetID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return api.MarketSummary{}, api.ErrNotFound
		}
		return api.MarketSummary{}, err
	}
	cs, err := s.Candles(ctx, assetID, tf)
	if err != nil {
		return api.MarketSummary{}, err
	}
	last, prev := cs.Candles[len(cs.Candles)-1], cs.Candles[len(cs.Candles)-2]
	change := last.Close - prev.Close
	return api.MarketSummary{
		Asset:         asset,
		Timeframe:     tf,
		LastClose:     last.Close,
		Change:        change,
		ChangePercent: change / prev.Close,
		AsOf:          last.Time.UTC().Format(time.RFC3339),
		Status:        "Historical", // real historical dataset; no live feed in V1
	}, nil
}

// seriesFromCandles wraps candles with EMA20/EMA50 overlays computed from
// real closes.
func seriesFromCandles(assetID string, tf market.Timeframe, candles []market.Candle) api.CandleSeries {
	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}
	toPoints := func(vals []float64, warmup int) []api.SeriesPoint {
		pts := make([]api.SeriesPoint, 0, len(vals)-warmup+1)
		for i := warmup - 1; i < len(vals); i++ {
			pts = append(pts, api.SeriesPoint{
				Time:  candles[i].Time.UTC().Format(time.RFC3339),
				Value: round2(vals[i]),
			})
		}
		return pts
	}
	return api.CandleSeries{
		AssetID:   assetID,
		Timeframe: tf,
		Candles:   candles,
		Overlays: api.Overlays{
			EMA20: toPoints(indicators.EMA(closes, 20), 20),
			EMA50: toPoints(indicators.EMA(closes, 50), 50),
		},
	}
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
