package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/backtest"
	"github.com/gold-mine-lab/backend/internal/metrics"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

var engineVersion = backtest.EngineVersion

// --- backtests ---------------------------------------------------------------

func (s *Service) ListRuns(ctx context.Context, strategyID string) ([]api.BacktestRun, error) {
	rows, err := s.repos.Runs.ListRuns(ctx, strategyID)
	if err != nil {
		return nil, err
	}
	out := make([]api.BacktestRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, runDTO(r))
	}
	return out, nil
}

func (s *Service) GetRun(ctx context.Context, runID string) (api.BacktestDetail, error) {
	r, err := s.repos.Runs.GetRun(ctx, runID)
	if errors.Is(err, repository.ErrNotFound) {
		return api.BacktestDetail{}, api.ErrNotFound
	}
	if err != nil {
		return api.BacktestDetail{}, err
	}
	detail := api.BacktestDetail{Run: runDTO(r)}

	if def, err := s.repos.Strategies.GetStrategy(ctx, r.StrategyID); err == nil {
		detail.Strategy = &def
	}
	if r.Status == "completed" {
		// Every completed run writes metrics; absent/unreadable metrics on a
		// completed run is an interrupted write — report it, don't hide it.
		m, err := s.repos.Metrics.GetMetrics(ctx, r.ID)
		if err != nil {
			return detail, fmt.Errorf("completed run metrics unreadable: %w", err)
		}
		if len(m) == 0 {
			return detail, fmt.Errorf("completed run %s has no persisted metrics", r.ID)
		}
		mm := metricsDTO(m)
		detail.Metrics = &mm
	}
	for _, p := range r.EquityCurve {
		detail.Equity = append(detail.Equity, api.EquityPoint{
			Time: p.Time.UTC().Format("2006-01-02T15:04:05Z"), Equity: p.Equity, Drawdown: p.Drawdown,
		})
	}
	trades, err := s.repos.Trades.ListTradesByRun(ctx, r.ID)
	if err != nil {
		return detail, err
	}
	for _, t := range trades {
		detail.Trades = append(detail.Trades, tradeDTO(t))
	}
	return detail, nil
}

func (s *Service) LatestRunID(ctx context.Context) (string, error) {
	return s.repos.Runs.LatestCompletedRunID(ctx)
}

// DashboardSummary is deliberately NOT a portfolio aggregate: independent
// runs (different parameters, windows, capital) cannot be summed into one
// account. totalBacktests is the experiment count; the performance figures
// and equity curve describe the latest completed run only.
func (s *Service) DashboardSummary(ctx context.Context) (api.DashboardSummary, error) {
	sum := api.DashboardSummary{}

	runs, err := s.repos.Runs.ListRuns(ctx, "")
	if err != nil {
		return sum, err
	}
	for _, r := range runs {
		if r.Status == "completed" {
			sum.TotalBacktests++
		}
	}

	latestID, err := s.repos.Runs.LatestCompletedRunID(ctx)
	if err != nil {
		return sum, err
	}
	if latestID == "" {
		return sum, nil
	}
	latest, err := s.repos.Runs.GetRun(ctx, latestID)
	if err != nil {
		return sum, err
	}
	sum.LatestRunID = &latest.ID
	sum.LatestRunStrategyID = &latest.StrategyID
	if def, err := s.repos.Strategies.GetStrategy(ctx, latest.StrategyID); err == nil {
		name := def.Name
		sum.LatestRunStrategyName = &name
	}
	if m, err := s.repos.Metrics.GetMetrics(ctx, latest.ID); err == nil {
		sum.NetPnl = m["net_pnl"]
		sum.WinRate = m["win_rate"]
		sum.MaxDrawdown = m["max_drawdown"]
	}
	for _, p := range latest.EquityCurve {
		sum.Equity = append(sum.Equity, api.EquityPoint{
			Time: p.Time.UTC().Format("2006-01-02T15:04:05Z"), Equity: p.Equity, Drawdown: p.Drawdown,
		})
	}
	return sum, nil
}

// Execute runs the real engine synchronously:
// queued → running → engine over the CSV window → persist trades+metrics+curve
// in one transaction → completed. Failures mark the run failed with a safe
// message.
func (s *Service) Execute(ctx context.Context, req api.BacktestRequest) (api.BacktestRun, error) {
	run, strat, err := s.validateRequest(ctx, req)
	if err != nil {
		return api.BacktestRun{}, err
	}

	// Provenance: fingerprint the exact CSV file this run will read. The
	// provider hashes each file at most once per process and caches it.
	if asset, err := s.repos.Assets.GetAsset(ctx, req.AssetID); err == nil {
		if fp, err := s.data.Fingerprint(asset, req.Timeframe); err == nil {
			run.Dataset = &fp
		}
	} // fingerprint unavailable → leave NULL rather than invent provenance

	runID, err := s.repos.Runs.CreateRun(ctx, run)
	if err != nil {
		return api.BacktestRun{}, fmt.Errorf("create run: %w", err)
	}
	run.ID = runID
	s.log.Info("backtest started", "run_id", runID, "strategy_id", req.StrategyID, "asset_id", req.AssetID)

	if err := s.repos.Runs.MarkRunRunning(ctx, runID); err != nil {
		return api.BacktestRun{}, fmt.Errorf("start run: %w", err)
	}

	res, trades, metricsRows, equity, err := s.runEngine(ctx, req, strat)
	if err != nil {
		msg := "backtest failed"
		var ve *api.ValidationError
		if errors.As(err, &ve) {
			msg = ve.Msg
		}
		_ = s.repos.Runs.FailRun(ctx, runID, msg)
		s.log.Warn("backtest failed", "run_id", runID, "error", err.Error())
		if ve != nil {
			return api.BacktestRun{}, err
		}
		return api.BacktestRun{}, fmt.Errorf("backtest execution failed")
	}

	if err := s.repos.Runs.CompleteRun(ctx, runID, trades, metricsRows, equity); err != nil {
		_ = s.repos.Runs.FailRun(ctx, runID, "persistence failed")
		s.log.Error("backtest persist failed", "run_id", runID, "error", err.Error())
		return api.BacktestRun{}, fmt.Errorf("persist results: %w", err)
	}
	s.log.Info("backtest completed", "run_id", runID,
		"trades", len(res.Trades), "final_equity", res.Equity[len(res.Equity)-1].Equity)

	row, err := s.repos.Runs.GetRun(ctx, runID)
	if err != nil {
		return api.BacktestRun{}, err
	}
	return runDTO(row), nil
}

func (s *Service) validateRequest(ctx context.Context, req api.BacktestRequest) (repository.RunRow, strategy.Strategy, error) {
	verr := func(msg string) (repository.RunRow, strategy.Strategy, error) {
		return repository.RunRow{}, nil, &api.ValidationError{Msg: msg}
	}
	if _, err := s.repos.Assets.GetAsset(ctx, req.AssetID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return verr("unknown assetId")
		}
		return repository.RunRow{}, nil, err
	}
	base, ok := s.registry.Get(req.StrategyID)
	if !ok {
		return verr("unknown strategyId")
	}
	supported := false
	for _, a := range base.SupportedAssets() {
		if a == req.AssetID {
			supported = true
		}
	}
	if !supported {
		return verr("strategy does not support this asset")
	}
	// Timeframe is a per-run choice constrained to the strategy's advertised
	// supported set (its default is Definition.Timeframe).
	def := describe(base)
	if len(def.SupportedTimeframes) > 0 {
		tfOK := false
		for _, t := range def.SupportedTimeframes {
			if t == req.Timeframe {
				tfOK = true
			}
		}
		if !tfOK {
			return verr(fmt.Sprintf("timeframe %s is not supported by %s (supported: %v)",
				req.Timeframe, def.ID, def.SupportedTimeframes))
		}
	}
	strat, err := configureStrategy(base, req.Parameters)
	if err != nil {
		return verr(err.Error())
	}
	start, err1 := time.Parse("2006-01-02", req.StartDate)
	end, err2 := time.Parse("2006-01-02", req.EndDate)
	if err1 != nil || err2 != nil {
		return verr("invalid startDate/endDate")
	}
	if !end.After(start) {
		return verr("endDate must be after startDate")
	}
	if req.InitialCapital <= 0 {
		return verr("initialCapital must be positive")
	}
	if req.PositionSize <= 0 || req.PositionSize > 100 {
		return verr("positionSize must be in (0, 100]")
	}
	if req.Commission < 0 || req.Commission > 10 {
		return verr("commission must be in [0, 10]")
	}
	if req.Slippage < 0 || req.Slippage > 10 {
		return verr("slippage must be in [0, 10]")
	}
	if _, ok := serveCaps[req.Timeframe]; !ok {
		return verr("unsupported timeframe")
	}
	return repository.RunRow{
		StrategyID: req.StrategyID, AssetID: req.AssetID, Timeframe: string(req.Timeframe),
		StartDate: req.StartDate, EndDate: req.EndDate,
		InitialCapital: req.InitialCapital, PositionSize: req.PositionSize,
		Commission: req.Commission, Slippage: req.Slippage, Parameters: req.Parameters,
		EngineVersion:   &engineVersion,
		StrategyVersion: ptr(strat.Version()), // captured at execution time
		ComparisonID:    req.ComparisonID,
		WalkForwardID:   req.WalkForwardID,
		Sample:          req.Sample,
	}, strat, nil
}

func ptr[T any](v T) *T { return &v }

// runEngine loads the candle window (plus indicator warm-up) and executes.
func (s *Service) runEngine(ctx context.Context, req api.BacktestRequest, strat strategy.Strategy) (backtest.Result, []repository.TradeRow, []repository.MetricValue, []repository.EquityPointRow, error) {
	var zero backtest.Result
	asset, _ := s.repos.Assets.GetAsset(ctx, req.AssetID)
	start, _ := time.Parse("2006-01-02", req.StartDate)
	end, _ := time.Parse("2006-01-02", req.EndDate)
	to := end.AddDate(0, 0, 1) // end_date inclusive → exclusive upper bound

	// Pre-window warm-up comes from the configured strategy (Warmupper) —
	// ema-cross asks for 3× slow_period for EMA convergence — never a fixed
	// constant that could mismatch the effective parameters.
	warmup := 60
	if w, ok := strat.(strategy.Warmupper); ok {
		warmup = w.WarmupBars()
	}
	candles, err := s.data.Range(asset, req.Timeframe, start, to, warmup)
	if err != nil {
		return zero, nil, nil, nil, &api.ValidationError{Msg: fmt.Sprintf("no candle data for window: %v", err)}
	}
	windowStart := 0
	for windowStart < len(candles) && candles[windowStart].Time.Before(start) {
		windowStart++
	}
	if len(candles)-windowStart < 5 {
		return zero, nil, nil, nil, &api.ValidationError{Msg: "window too small for a backtest"}
	}

	res, err := s.engine.Run(ctx, backtest.Input{
		Strategy: strat, Candles: candles, WindowStart: windowStart,
		InitialCapital: req.InitialCapital, PositionSize: req.PositionSize,
		Commission: req.Commission, Slippage: req.Slippage,
	})
	if err != nil {
		return zero, nil, nil, nil, err
	}

	trades := make([]repository.TradeRow, 0, len(res.Trades))
	for _, t := range res.Trades {
		trades = append(trades, repository.TradeRow{
			StrategyID: req.StrategyID, AssetID: req.AssetID, Side: t.Side,
			EntryTime: t.EntryTime, EntryPrice: t.EntryPrice,
			ExitTime: t.ExitTime, ExitPrice: t.ExitPrice,
			Quantity: t.Quantity, GrossPnl: t.GrossPnl, Pnl: t.Pnl,
			EntryCommission: &t.EntryCommission, ExitCommission: &t.ExitCommission,
			Status: t.Status, Result: t.Result,
		})
	}
	m := metrics.Compute(res, req.InitialCapital)
	equity := make([]repository.EquityPointRow, 0, len(res.Equity))
	for _, p := range res.Equity {
		equity = append(equity, repository.EquityPointRow{Time: p.Time, Equity: p.Equity, Drawdown: p.Drawdown})
	}
	return res, trades, m.Rows(), equity, nil
}

func configureStrategy(base strategy.Strategy, params map[string]any) (strategy.Strategy, error) {
	if cfg, ok := base.(strategy.Configurable); ok {
		return cfg.Configure(params)
	}
	return base, nil
}

// --- mappers -----------------------------------------------------------------

func runDTO(r repository.RunRow) api.BacktestRun {
	var completedAt *string
	if r.CompletedAt != nil {
		s := r.CompletedAt.UTC().Format(time.RFC3339)
		completedAt = &s
	}
	return api.BacktestRun{
		ID: r.ID, StrategyID: r.StrategyID, AssetID: r.AssetID,
		Timeframe: tfOf(r.Timeframe), StartDate: r.StartDate, EndDate: r.EndDate,
		InitialCapital: r.InitialCapital, PositionSize: r.PositionSize,
		Commission: r.Commission, Slippage: r.Slippage, Parameters: r.Parameters,
		Status: r.Status, CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		CompletedAt: completedAt, Error: r.Error,
		EngineVersion: r.EngineVersion, StrategyVersion: r.StrategyVersion, Dataset: r.Dataset,
		ComparisonID: r.ComparisonID, WalkForwardID: r.WalkForwardID, Sample: r.Sample,
	}
}

func tradeDTO(t repository.TradeRow) api.BacktestTrade {
	var exitTime *string
	if t.ExitTime != nil {
		s := t.ExitTime.UTC().Format(time.RFC3339)
		exitTime = &s
	}
	return api.BacktestTrade{
		ID: t.ID, RunID: t.RunID, StrategyID: t.StrategyID, AssetID: t.AssetID,
		Side: t.Side, EntryTime: t.EntryTime.UTC().Format(time.RFC3339),
		EntryPrice: t.EntryPrice, ExitTime: exitTime, ExitPrice: t.ExitPrice,
		Quantity: t.Quantity, GrossPnl: t.GrossPnl, Pnl: t.Pnl,
		EntryCommission: t.EntryCommission, ExitCommission: t.ExitCommission,
		Status: t.Status, Result: t.Result,
	}
}
