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
	if m, err := s.repos.Metrics.GetMetrics(ctx, r.ID); err == nil && len(m) > 0 {
		detail.Metrics = &api.BacktestMetrics{
			NetPnl: m["net_pnl"], ReturnPct: m["return_pct"], WinRate: m["win_rate"],
			ProfitFactor: m["profit_factor"], MaxDrawdown: m["max_drawdown"],
			TotalTrades: int(m["total_trades"]), AvgTradePnl: m["avg_trade_pnl"],
		}
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

// DashboardSummary aggregates persisted run metrics. The equity curve is a
// cumulative-P&L account view: every completed run's initial capital summed
// as the base, then all trades' realized P&L applied in exit-time order.
func (s *Service) DashboardSummary(ctx context.Context) (api.DashboardSummary, error) {
	sum := api.DashboardSummary{}

	netPnl, winRateMean, maxDD, totalRuns, err := s.repos.Metrics.AggregateMetrics(ctx)
	if err != nil {
		return sum, err
	}
	sum.TotalBacktests = totalRuns
	sum.NetPnl = netPnl
	sum.WinRate = winRateMean
	sum.MaxDrawdown = maxDD

	bestID, bestName, ok, err := s.repos.Metrics.BestStrategyByNetPnl(ctx)
	if err != nil {
		return sum, err
	}
	if ok {
		sum.BestStrategyID, sum.BestStrategyName = &bestID, &bestName
	}

	// Aggregate equity: base capital = Σ completed runs' initial capital;
	// then cumulative realized P&L at each trade's exit time.
	runs, err := s.repos.Runs.ListRuns(ctx, "")
	if err != nil {
		return sum, err
	}
	base := 0.0
	for _, r := range runs {
		if r.Status == "completed" {
			base += r.InitialCapital
		}
	}
	trades, err := s.repos.Trades.ListAllClosed(ctx)
	if err != nil {
		return sum, err
	}
	equity, peak := base, base
	if len(trades) > 0 {
		sum.Equity = append(sum.Equity, api.EquityPoint{
			Time: trades[0].EntryTime.UTC().Format("2006-01-02T15:04:05Z"), Equity: base,
		})
	}
	for _, t := range trades {
		equity += *t.Pnl
		if equity > peak {
			peak = equity
		}
		dd := 0.0
		if peak > 0 {
			dd = (equity - peak) / peak
		}
		ts := t.ExitTime
		if ts == nil {
			ts = &t.EntryTime
		}
		sum.Equity = append(sum.Equity, api.EquityPoint{
			Time: ts.UTC().Format("2006-01-02T15:04:05Z"), Equity: equity, Drawdown: dd,
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
	}, strat, nil
}

// runEngine loads the candle window (plus indicator warm-up) and executes.
func (s *Service) runEngine(ctx context.Context, req api.BacktestRequest, strat strategy.Strategy) (backtest.Result, []repository.TradeRow, []repository.MetricValue, []repository.EquityPointRow, error) {
	var zero backtest.Result
	asset, _ := s.repos.Assets.GetAsset(ctx, req.AssetID)
	start, _ := time.Parse("2006-01-02", req.StartDate)
	end, _ := time.Parse("2006-01-02", req.EndDate)
	to := end.AddDate(0, 0, 1) // end_date inclusive → exclusive upper bound

	warmup := 120 // covers the slowest EMA period allowed (slow<=100)
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
			Quantity: t.Quantity, Pnl: t.Pnl, Status: t.Status, Result: t.Result,
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
		Quantity: t.Quantity, Pnl: t.Pnl, Status: t.Status, Result: t.Result,
	}
}
