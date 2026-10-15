package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/repository"
)

// --- walk-forward research (Phase 5) -----------------------------------------
//
// An experiment replays each leg's FIXED parameter set over rolling
// (in-sample, out-of-sample) folds — robustness analysis, not optimization:
// no parameters are fitted, nothing learns from OOS results, and each fold
// is an independent engine run through the standard Execute path.

type foldWindow struct {
	Index    int
	IsStart  time.Time
	IsEnd    time.Time
	OosStart time.Time
	OosEnd   time.Time
}

// generateFolds rolls (isMonths, oosMonths) windows forward by stepMonths
// over [start, end]. OOS windows never overlap when step >= oos (enforced);
// a fold is emitted only while its OOS window fits inside the range.
func generateFolds(start, end time.Time, isMonths, oosMonths, stepMonths int) []foldWindow {
	var folds []foldWindow
	for i := 0; ; i++ {
		isStart := start.AddDate(0, i*stepMonths, 0)
		isEnd := isStart.AddDate(0, isMonths, 0)
		oosEnd := isEnd.AddDate(0, oosMonths, 0)
		if oosEnd.After(end) {
			break
		}
		folds = append(folds, foldWindow{Index: i, IsStart: isStart, IsEnd: isEnd, OosStart: isEnd, OosEnd: oosEnd})
	}
	return folds
}

func (s *Service) validateWalkForward(req api.WalkForwardRequest) ([]foldWindow, error) {
	verr := func(format string, a ...any) ([]foldWindow, error) {
		return nil, &api.ValidationError{Msg: fmt.Sprintf(format, a...)}
	}
	if len(req.Legs) < 1 {
		return verr("a walk-forward needs at least 1 strategy")
	}
	if len(req.Legs) > 4 {
		return verr("a walk-forward supports at most 4 strategies")
	}
	seen := map[string]bool{}
	for _, l := range req.Legs {
		if seen[l.StrategyID] {
			return verr("duplicate strategy: " + l.StrategyID)
		}
		seen[l.StrategyID] = true
	}
	start, e1 := time.Parse("2006-01-02", req.StartDate)
	end, e2 := time.Parse("2006-01-02", req.EndDate)
	if e1 != nil || e2 != nil || !end.After(start) {
		return verr("invalid research range startDate/endDate")
	}
	if req.InSampleMonths < 1 || req.OosMonths < 1 {
		return verr("in-sample and OOS lengths must be >= 1 month")
	}
	step := req.StepMonths
	if step == 0 {
		step = req.OosMonths
	}
	if step < req.OosMonths {
		return verr("step must be >= OOS length so OOS windows never overlap")
	}
	folds := generateFolds(start, end, req.InSampleMonths, req.OosMonths, step)
	if len(folds) == 0 {
		return verr("range too short: no complete in-sample + OOS fold fits")
	}
	return folds, nil
}

func (s *Service) wfLegRequest(req api.WalkForwardRequest, leg api.ComparisonLeg, start, end time.Time, expID *string, sample string) api.BacktestRequest {
	return api.BacktestRequest{
		StrategyID: leg.StrategyID, AssetID: req.AssetID, Timeframe: req.Timeframe,
		StartDate: start.Format("2006-01-02"), EndDate: end.Format("2006-01-02"),
		InitialCapital: req.InitialCapital, PositionSize: req.PositionSize,
		Commission: req.Commission, Slippage: req.Slippage,
		Parameters: leg.Parameters, WalkForwardID: expID, Sample: &sample,
	}
}

// PreviewWalkForward validates the request and returns the fold boundaries
// without persisting or running anything.
func (s *Service) PreviewWalkForward(ctx context.Context, req api.WalkForwardRequest) (api.WalkForward, error) {
	folds, err := s.validateWalkForward(req)
	if err != nil {
		return api.WalkForward{}, err
	}
	step := req.StepMonths
	if step == 0 {
		step = req.OosMonths
	}
	out := api.WalkForward{
		Status: "preview", AssetID: req.AssetID, Timeframe: string(req.Timeframe),
		StartDate: req.StartDate, EndDate: req.EndDate,
		InSampleMonths: req.InSampleMonths, OosMonths: req.OosMonths, StepMonths: step,
		InitialCapital: req.InitialCapital, PositionSize: req.PositionSize,
		Commission: req.Commission, Slippage: req.Slippage,
	}
	for _, f := range folds {
		for _, l := range req.Legs {
			out.Folds = append(out.Folds, api.WalkForwardFold{
				FoldIndex: f.Index, StrategyID: l.StrategyID,
				IsStart: f.IsStart.Format("2006-01-02"), IsEnd: f.IsEnd.Format("2006-01-02"),
				OosStart: f.OosStart.Format("2006-01-02"), OosEnd: f.OosEnd.Format("2006-01-02"),
				Status: "pending",
			})
		}
	}
	return out, nil
}

// ExecuteWalkForward validates every leg×fold×window, persists the
// experiment and all fold boundaries up front, then runs each member run
// synchronously through Execute (folds are independent engine runs — no
// position or state carries between folds or samples).
func (s *Service) ExecuteWalkForward(ctx context.Context, req api.WalkForwardRequest) (api.WalkForward, error) {
	folds, err := s.validateWalkForward(req)
	if err != nil {
		return api.WalkForward{}, err
	}
	verr := func(format string, a ...any) (api.WalkForward, error) {
		return api.WalkForward{}, &api.ValidationError{Msg: fmt.Sprintf(format, a...)}
	}
	// Reject invalid legs/windows up front — nothing is persisted for a
	// request the engine would reject anyway.
	for _, leg := range req.Legs {
		for _, f := range folds {
			for _, w := range [][2]time.Time{{f.IsStart, f.IsEnd}, {f.OosStart, f.OosEnd}} {
				if _, _, err := s.validateRequest(ctx, s.wfLegRequest(req, leg, w[0], w[1], nil, "")); err != nil {
					var ve *api.ValidationError
					if errors.As(err, &ve) {
						return verr("%s fold %d: %s", leg.StrategyID, f.Index, ve.Msg)
					}
					return api.WalkForward{}, err
				}
			}
		}
	}
	step := req.StepMonths
	if step == 0 {
		step = req.OosMonths
	}
	strategyIDs := make([]string, 0, len(req.Legs))
	for _, l := range req.Legs {
		strategyIDs = append(strategyIDs, l.StrategyID)
	}
	expID, err := s.repos.WalkForwards.CreateExperiment(ctx, repository.WalkForwardExperimentRow{
		AssetID: req.AssetID, Timeframe: string(req.Timeframe),
		RangeStart: req.StartDate, RangeEnd: req.EndDate,
		IsMonths: req.InSampleMonths, OosMonths: req.OosMonths, StepMonths: step,
		InitialCapital: req.InitialCapital, PositionSize: req.PositionSize,
		Commission: req.Commission, Slippage: req.Slippage,
		StrategyIDs: strategyIDs, Status: "pending",
	})
	if err != nil {
		return api.WalkForward{}, fmt.Errorf("create experiment: %w", err)
	}
	if err := s.repos.WalkForwards.SetExperimentStatus(ctx, expID, "running", nil); err != nil {
		return api.WalkForward{}, fmt.Errorf("start experiment: %w", err)
	}
	s.log.Info("walk-forward started", "experiment_id", expID, "folds", len(folds), "strategies", len(req.Legs))

	// Persist every fold boundary first so the experiment is inspectable
	// even while/in case it fails partway.
	foldIDs := map[string]string{} // "foldIdx|strategy" → fold row id
	for _, f := range folds {
		for _, leg := range req.Legs {
			fid, err := s.repos.WalkForwards.CreateFold(ctx, repository.WalkForwardFoldRow{
				ExperimentID: expID, FoldIndex: f.Index, StrategyID: leg.StrategyID,
				IsStart: f.IsStart.Format("2006-01-02"), IsEnd: f.IsEnd.Format("2006-01-02"),
				OosStart: f.OosStart.Format("2006-01-02"), OosEnd: f.OosEnd.Format("2006-01-02"),
			})
			if err != nil {
				return api.WalkForward{}, fmt.Errorf("create fold %d %s: %w", f.Index, leg.StrategyID, err)
			}
			foldIDs[fmt.Sprintf("%d|%s", f.Index, leg.StrategyID)] = fid
		}
	}

	total, failed := 0, 0
	var firstErr string
	for _, f := range folds {
		for _, leg := range req.Legs {
			total++
			fid := foldIDs[fmt.Sprintf("%d|%s", f.Index, leg.StrategyID)]
			var isID, oosID *string
			var foldErr string
			for _, w := range []struct {
				sample     string
				start, end time.Time
				runID      **string
			}{
				{"in_sample", f.IsStart, f.IsEnd, &isID},
				{"out_of_sample", f.OosStart, f.OosEnd, &oosID},
			} {
				br := s.wfLegRequest(req, leg, w.start, w.end, &expID, w.sample)
				run, err := s.Execute(ctx, br)
				if err != nil {
					if foldErr == "" {
						foldErr = fmt.Sprintf("%s: %s", w.sample, err.Error())
					}
					s.log.Warn("walk-forward run failed", "experiment_id", expID,
						"fold", f.Index, "strategy_id", leg.StrategyID, "sample", w.sample, "error", err.Error())
					continue
				}
				id := run.ID
				*w.runID = &id
			}
			if foldErr != "" {
				failed++
				if firstErr == "" {
					firstErr = fmt.Sprintf("fold %d %s: %s", f.Index, leg.StrategyID, foldErr)
				}
				errCopy := foldErr
				_ = s.repos.WalkForwards.SetFoldResult(ctx, fid, "failed", isID, oosID, &errCopy)
			} else {
				_ = s.repos.WalkForwards.SetFoldResult(ctx, fid, "completed", isID, oosID, nil)
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
	if err := s.repos.WalkForwards.SetExperimentStatus(ctx, expID, status, errMsg); err != nil {
		return api.WalkForward{}, fmt.Errorf("finalize experiment: %w", err)
	}
	s.log.Info("walk-forward finished", "experiment_id", expID, "status", status, "folds", total, "failed", failed)
	return s.GetWalkForward(ctx, expID)
}

// ListWalkForwards returns saved experiments newest-first.
func (s *Service) ListWalkForwards(ctx context.Context) ([]api.WalkForward, error) {
	rows, err := s.repos.WalkForwards.ListExperiments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.WalkForward, 0, len(rows))
	for _, r := range rows {
		out = append(out, walkForwardDTO(r, nil, nil))
	}
	return out, nil
}

// GetWalkForward returns the experiment with each fold's member runs,
// metrics and equity curves, plus per-strategy OOS robustness summaries.
func (s *Service) GetWalkForward(ctx context.Context, id string) (api.WalkForward, error) {
	exp, err := s.repos.WalkForwards.GetExperiment(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return api.WalkForward{}, api.ErrNotFound
	}
	if err != nil {
		return api.WalkForward{}, err
	}
	foldRows, err := s.repos.WalkForwards.ListFolds(ctx, id)
	if err != nil {
		return api.WalkForward{}, err
	}
	folds := make([]api.WalkForwardFold, 0, len(foldRows))
	sums := map[string]*api.WalkForwardSummary{}
	rets := map[string][]float64{}
	for _, fr := range foldRows {
		f := api.WalkForwardFold{
			FoldIndex: fr.FoldIndex, StrategyID: fr.StrategyID,
			IsStart: fr.IsStart, IsEnd: fr.IsEnd, OosStart: fr.OosStart, OosEnd: fr.OosEnd,
			Status: fr.Status, Error: fr.Error,
		}
		if fr.IsRunID != nil {
			if res, err := s.comparisonResult(ctx, *fr.IsRunID); err == nil {
				f.IsResult = &res
			}
		}
		if fr.OosRunID != nil {
			if res, err := s.comparisonResult(ctx, *fr.OosRunID); err == nil {
				f.OosResult = &res
			}
		}
		folds = append(folds, f)

		sum, ok := sums[fr.StrategyID]
		if !ok {
			sum = &api.WalkForwardSummary{}
			sums[fr.StrategyID] = sum
		}
		if fr.Status == "failed" {
			sum.FailedFolds++
		} else if f.OosResult != nil && f.OosResult.Metrics != nil {
			m := f.OosResult.Metrics
			sum.CompletedFolds++
			if m.NetPnl > 0 {
				sum.PositiveFolds++
			}
			sum.TotalTrades += m.TotalTrades
			if m.TotalTrades < 30 {
				sum.LowTradeFolds++
			}
			rets[fr.StrategyID] = append(rets[fr.StrategyID], m.ReturnPct)
			if sum.CompletedFolds == 1 || m.ReturnPct < sum.WorstReturnPct {
				sum.WorstReturnPct = m.ReturnPct
			}
			if m.MaxDrawdown > sum.WorstMaxDrawdown {
				sum.WorstMaxDrawdown = m.MaxDrawdown
			}
		}
	}
	for sid, sum := range sums {
		if v := rets[sid]; len(v) > 0 {
			sort.Float64s(v)
			n := len(v)
			sum.MedianReturnPct = v[n/2]
			if n%2 == 0 {
				sum.MedianReturnPct = (v[n/2-1] + v[n/2]) / 2
			}
		}
		// Inconclusive when most completed OOS folds are below the 30-trade
		// evidence threshold.
		sum.Inconclusive = sum.CompletedFolds == 0 || sum.LowTradeFolds*2 > sum.CompletedFolds
	}
	return walkForwardDTO(exp, folds, sums), nil
}

// comparisonResult loads one run's metrics + equity for fold/leg display.
func (s *Service) comparisonResult(ctx context.Context, runID string) (api.ComparisonResult, error) {
	r, err := s.repos.Runs.GetRun(ctx, runID)
	if err != nil {
		return api.ComparisonResult{}, err
	}
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
	return res, nil
}

func walkForwardDTO(e repository.WalkForwardExperimentRow, folds []api.WalkForwardFold, sums map[string]*api.WalkForwardSummary) api.WalkForward {
	var completedAt *string
	if e.CompletedAt != nil {
		t := e.CompletedAt.UTC().Format("2006-01-02T15:04:05Z")
		completedAt = &t
	}
	return api.WalkForward{
		ID: e.ID, Status: e.Status, AssetID: e.AssetID, Timeframe: e.Timeframe,
		StartDate: e.RangeStart, EndDate: e.RangeEnd,
		InSampleMonths: e.IsMonths, OosMonths: e.OosMonths, StepMonths: e.StepMonths,
		InitialCapital: e.InitialCapital, PositionSize: e.PositionSize,
		Commission: e.Commission, Slippage: e.Slippage,
		StrategyIDs: e.StrategyIDs, Error: e.Error,
		CreatedAt:   e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		CompletedAt: completedAt, Folds: folds, Summaries: sums,
	}
}
