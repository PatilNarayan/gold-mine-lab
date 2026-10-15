package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/repository"
)

// --- comparisons (Phase 4) ---------------------------------------------------
//
// A comparison groups 2+ strategy runs executed under IDENTICAL assumptions:
// same dataset fingerprint, window, capital, sizing, costs, engine version.
// Each leg runs the standard engine via Execute — no duplicated accounting.
// Optional out-of-sample legs replay the same configuration on a strictly
// later window, so warm-up history may extend into the in-sample range but
// trades/metrics belong only to each leg's own window.

// ExecuteComparison validates the legs, persists the comparison row, then
// runs each strategy synchronously (in-sample, then out-of-sample if given).
// Status: completed (all legs ok), partial_failed (some failed), failed (all
// failed or a leg was rejected by validation).
func (s *Service) ExecuteComparison(ctx context.Context, req api.ComparisonRequest) (api.Comparison, error) {
	verr := func(msg string) (api.Comparison, error) {
		return api.Comparison{}, &api.ValidationError{Msg: msg}
	}
	// --- request validation --------------------------------------------------
	if len(req.Legs) < 2 {
		return verr("a comparison needs at least 2 strategies")
	}
	if len(req.Legs) > 4 {
		return verr("a comparison supports at most 4 strategies")
	}
	seen := map[string]bool{}
	strategyIDs := make([]string, 0, len(req.Legs))
	for _, l := range req.Legs {
		if seen[l.StrategyID] {
			return verr("duplicate strategy in comparison: " + l.StrategyID)
		}
		seen[l.StrategyID] = true
		strategyIDs = append(strategyIDs, l.StrategyID)
	}
	if _, err := time.Parse("2006-01-02", req.StartDate); err != nil {
		return verr("invalid startDate")
	}
	endD, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		return verr("invalid endDate")
	}
	hasOOS := req.OutOfSampleStartDate != nil || req.OutOfSampleEndDate != nil
	if hasOOS {
		if req.OutOfSampleStartDate == nil || req.OutOfSampleEndDate == nil {
			return verr("out-of-sample needs both oosStartDate and oosEndDate")
		}
		osStart, e1 := time.Parse("2006-01-02", *req.OutOfSampleStartDate)
		osEnd, e2 := time.Parse("2006-01-02", *req.OutOfSampleEndDate)
		if e1 != nil || e2 != nil {
			return verr("invalid out-of-sample dates")
		}
		if !osStart.After(endD) {
			return verr("out-of-sample window must start strictly after the in-sample endDate")
		}
		if osEnd.Before(osStart) {
			return verr("oosEndDate before oosStartDate")
		}
	}

	row := repository.ComparisonRow{
		AssetID: req.AssetID, Timeframe: string(req.Timeframe),
		StartDate: req.StartDate, EndDate: req.EndDate,
		OosStartDate: req.OutOfSampleStartDate, OosEndDate: req.OutOfSampleEndDate,
		InitialCapital: req.InitialCapital, PositionSize: req.PositionSize,
		Commission: req.Commission, Slippage: req.Slippage,
		StrategyIDs: strategyIDs, Status: "pending",
	}
	cmpID, err := s.repos.Comparisons.CreateComparison(ctx, row)
	if err != nil {
		return api.Comparison{}, fmt.Errorf("create comparison: %w", err)
	}
	row.ID = cmpID
	s.log.Info("comparison started", "comparison_id", cmpID, "strategies", len(strategyIDs))

	// --- sequential leg execution ---------------------------------------------
	total, failed := 0, 0
	var firstErr string
	for _, leg := range req.Legs {
		for _, window := range s.comparisonWindows(req) {
			total++
			br := api.BacktestRequest{
				StrategyID: leg.StrategyID, AssetID: req.AssetID, Timeframe: req.Timeframe,
				StartDate: window.StartDate, EndDate: window.EndDate,
				InitialCapital: req.InitialCapital, PositionSize: req.PositionSize,
				Commission: req.Commission, Slippage: req.Slippage,
				Parameters:   leg.Parameters,
				ComparisonID: &cmpID, Sample: &window.Sample,
			}
			if _, err := s.Execute(ctx, br); err != nil {
				failed++
				if firstErr == "" {
					firstErr = fmt.Sprintf("%s/%s: %s", leg.StrategyID, window.Sample, err.Error())
				}
				s.log.Warn("comparison leg failed", "comparison_id", cmpID,
					"strategy_id", leg.StrategyID, "sample", window.Sample, "error", err.Error())
			}
		}
	}

	status := "completed"
	var errMsg *string
	switch {
	case failed == total:
		status = "failed"
		errMsg = &firstErr
	case failed > 0:
		status = "partial_failed"
		errMsg = &firstErr
	}
	if err := s.repos.Comparisons.SetComparisonStatus(ctx, cmpID, status, errMsg); err != nil {
		return api.Comparison{}, fmt.Errorf("finalize comparison: %w", err)
	}
	s.log.Info("comparison finished", "comparison_id", cmpID, "status", status, "legs", total, "failed", failed)

	return s.GetComparison(ctx, cmpID)
}

type sampleWindow struct{ Sample, StartDate, EndDate string }

func (s *Service) comparisonWindows(req api.ComparisonRequest) []sampleWindow {
	w := []sampleWindow{{Sample: "in_sample", StartDate: req.StartDate, EndDate: req.EndDate}}
	if req.OutOfSampleStartDate != nil && req.OutOfSampleEndDate != nil {
		w = append(w, sampleWindow{
			Sample: "out_of_sample", StartDate: *req.OutOfSampleStartDate, EndDate: *req.OutOfSampleEndDate,
		})
	}
	return w
}

// ListComparisons returns saved comparisons newest-first (no results payload).
func (s *Service) ListComparisons(ctx context.Context) ([]api.Comparison, error) {
	rows, err := s.repos.Comparisons.ListComparisons(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Comparison, 0, len(rows))
	for _, r := range rows {
		out = append(out, comparisonDTO(r, nil))
	}
	return out, nil
}

// GetComparison returns the comparison plus every member run's metrics and
// equity curve — each result identifies its own run, sample and provenance.
func (s *Service) GetComparison(ctx context.Context, id string) (api.Comparison, error) {
	row, err := s.repos.Comparisons.GetComparison(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return api.Comparison{}, api.ErrNotFound
	}
	if err != nil {
		return api.Comparison{}, err
	}
	runs, err := s.repos.Runs.ListRunsByComparison(ctx, id)
	if err != nil {
		return api.Comparison{}, err
	}
	results := make([]api.ComparisonResult, 0, len(runs))
	for _, r := range runs {
		res := api.ComparisonResult{Run: runDTO(r)}
		if r.Sample != nil {
			res.Sample = *r.Sample
		}
		if r.Status == "completed" {
			if m, err := s.repos.Metrics.GetMetrics(ctx, r.ID); err == nil {
				mm := metricsDTO(m)
				res.Metrics = &mm
			}
			for _, p := range r.EquityCurve {
				res.Equity = append(res.Equity, api.EquityPoint{
					Time: p.Time.UTC().Format("2006-01-02T15:04:05Z"), Equity: p.Equity, Drawdown: p.Drawdown,
				})
			}
		}
		results = append(results, res)
	}
	return comparisonDTO(row, results), nil
}

func metricsDTO(m map[string]float64) api.BacktestMetrics {
	out := api.BacktestMetrics{
		NetPnl:        m["net_pnl"],
		ReturnPct:     m["return_pct"],
		WinRate:       m["win_rate"],
		ProfitFactor:  m["profit_factor"],
		MaxDrawdown:   m["max_drawdown"],
		TotalTrades:   int(m["total_trades"]),
		WinningTrades: int(m["winning_trades"]),
		LosingTrades:  int(m["losing_trades"]),
		AvgTradePnl:   m["avg_trade_pnl"],
		GrossProfit:   m["gross_profit"],
		GrossLoss:     m["gross_loss"],
	}
	if v, ok := m["total_commissions"]; ok {
		out.TotalCommissions = &v
	}
	return out
}

func comparisonDTO(c repository.ComparisonRow, results []api.ComparisonResult) api.Comparison {
	var completedAt *string
	if c.CompletedAt != nil {
		t := c.CompletedAt.UTC().Format("2006-01-02T15:04:05Z")
		completedAt = &t
	}
	return api.Comparison{
		ID: c.ID, Status: c.Status, AssetID: c.AssetID, Timeframe: c.Timeframe,
		StartDate: c.StartDate, EndDate: c.EndDate,
		OosStartDate: c.OosStartDate, OosEndDate: c.OosEndDate,
		InitialCapital: c.InitialCapital, PositionSize: c.PositionSize,
		Commission: c.Commission, Slippage: c.Slippage,
		StrategyIDs: c.StrategyIDs, Error: c.Error,
		CreatedAt:   c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		CompletedAt: completedAt, Results: results,
	}
}
