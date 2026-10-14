package demo

import (
	"context"
	"sync"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// Service implements the api.*Service contracts over deterministic in-memory
// data. Swap it for Postgres-backed services in a later phase — the handlers
// and frontend do not change.
type Service struct {
	store *store

	mu      sync.Mutex // guards the caches below (concurrent handlers)
	candles map[string]api.CandleSeries
	signals []strategy.Signal
}

// New builds a demo service with all data lazily deterministic.
func New() *Service {
	return &Service{store: newStore(), candles: map[string]api.CandleSeries{}}
}

func (s *Service) series(assetID string, tf market.Timeframe) (api.CandleSeries, error) {
	key := assetID + "|" + string(tf)
	s.mu.Lock()
	defer s.mu.Unlock()
	if cs, ok := s.candles[key]; ok {
		return cs, nil
	}
	cs, err := candleSeries(assetID, tf)
	if err != nil {
		return api.CandleSeries{}, err
	}
	s.candles[key] = cs
	return cs, nil
}

// --- api.MarketService ---

func (s *Service) Assets(_ context.Context) ([]market.Asset, error) {
	return assets, nil
}

func (s *Service) Candles(_ context.Context, assetID string, tf market.Timeframe) (api.CandleSeries, error) {
	return s.series(assetID, tf)
}

func (s *Service) MarketSummary(_ context.Context, assetID string, tf market.Timeframe) (api.MarketSummary, error) {
	asset, ok := assetByID(assetID)
	if !ok {
		return api.MarketSummary{}, api.ErrNotFound
	}
	series, err := s.series(assetID, tf)
	if err != nil {
		return api.MarketSummary{}, err
	}
	cs := series.Candles
	last := cs[len(cs)-1]
	prev := cs[len(cs)-2]
	change := round(last.Close-prev.Close, meta(assetID).digits)
	return api.MarketSummary{
		Asset:         asset,
		Timeframe:     tf,
		LastClose:     last.Close,
		Change:        change,
		ChangePercent: round(change/prev.Close, 4),
		AsOf:          last.Time.UTC().Format(time.RFC3339),
		// Data is synthetic history — label it honestly even over the API.
		Status: "Historical / Demo",
	}, nil
}

// --- api.StrategyService ---

func (s *Service) List(_ context.Context) ([]api.StrategyListItem, error) {
	out := make([]api.StrategyListItem, 0, len(strategies))
	for _, def := range strategies {
		out = append(out, api.StrategyListItem{
			Definition:  def,
			LastSignal:  latestSignalFor(def.ID),
			Performance: s.store.performanceFor(def.ID),
		})
	}
	return out, nil
}

func (s *Service) Get(_ context.Context, id string) (strategy.Definition, error) {
	if def, ok := strategyByID(id); ok {
		return def, nil
	}
	return strategy.Definition{}, api.ErrNotFound
}

func (s *Service) Signals(_ context.Context, assetID, strategyID string, limit int) ([]strategy.Signal, error) {
	s.mu.Lock()
	if s.signals == nil {
		s.signals = allSignals()
	}
	signals := s.signals
	s.mu.Unlock()

	out := make([]strategy.Signal, 0)
	for _, sig := range signals { // sorted newest-first
		if assetID != "" && sig.AssetID != assetID {
			continue
		}
		if strategyID != "" && sig.StrategyID != strategyID {
			continue
		}
		out = append(out, sig)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- api.BacktestService ---

func (s *Service) ListRuns(_ context.Context, strategyID string) ([]api.BacktestRun, error) {
	return s.store.listRuns(strategyID), nil
}

func (s *Service) GetRun(_ context.Context, runID string) (api.BacktestDetail, error) {
	d, ok := s.store.detail(runID)
	if !ok {
		return api.BacktestDetail{}, api.ErrNotFound
	}
	return d, nil
}

func (s *Service) LatestRunID(_ context.Context) (string, error) {
	return s.store.latestCompletedRunID(), nil
}

func (s *Service) DashboardSummary(_ context.Context) (api.DashboardSummary, error) {
	return s.store.summary(), nil
}

func (s *Service) Execute(_ context.Context, req api.BacktestRequest) (api.BacktestRun, error) {
	if err := validateRunRequest(req); err != nil {
		return api.BacktestRun{}, err
	}
	return s.store.execute(req)
}

func validateRunRequest(req api.BacktestRequest) error {
	if _, ok := assetByID(req.AssetID); !ok {
		return &api.ValidationError{Msg: "unknown assetId " + req.AssetID}
	}
	if _, ok := strategyByID(req.StrategyID); !ok {
		return &api.ValidationError{Msg: "unknown strategyId " + req.StrategyID}
	}
	if _, ok := timeframeStep[req.Timeframe]; !ok {
		return &api.ValidationError{Msg: "invalid timeframe " + string(req.Timeframe)}
	}
	start, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		return &api.ValidationError{Msg: "invalid startDate"}
	}
	end, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		return &api.ValidationError{Msg: "invalid endDate"}
	}
	if !end.After(start) {
		return &api.ValidationError{Msg: "endDate must be after startDate"}
	}
	if req.InitialCapital <= 0 {
		return &api.ValidationError{Msg: "initialCapital must be positive"}
	}
	if req.PositionSize <= 0 {
		return &api.ValidationError{Msg: "positionSize must be positive"}
	}
	if req.Commission < 0 || req.Slippage < 0 {
		return &api.ValidationError{Msg: "commission and slippage cannot be negative"}
	}
	return nil
}

// --- api.TradeService ---

func (s *Service) ListTrades(_ context.Context, f api.TradeFilters) (api.PagedTrades, error) {
	filtered := make([]api.BacktestTrade, 0)
	for _, t := range s.store.allTrades() {
		if f.StrategyID != "" && t.StrategyID != f.StrategyID {
			continue
		}
		if f.Side != "" && t.Side != f.Side {
			continue
		}
		if f.Result != "" && t.Result != f.Result {
			continue
		}
		if f.From != "" && t.EntryTime < f.From {
			continue
		}
		if f.To != "" && t.EntryTime >= f.To {
			continue
		}
		filtered = append(filtered, t)
	}
	page := f.Page
	if page < 1 {
		page = 1
	}
	size := f.PageSize
	if size < 1 {
		size = 20
	}
	start := (page - 1) * size
	if start > len(filtered) {
		start = len(filtered)
	}
	end := min(start+size, len(filtered))
	return api.PagedTrades{
		Items:    filtered[start:end],
		Total:    len(filtered),
		Page:     page,
		PageSize: size,
	}, nil
}
