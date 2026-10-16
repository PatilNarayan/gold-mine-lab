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

// foldWindow holds HALF-OPEN fold boundaries: the in-sample window covers
// [IsStart, IsEndEx) and the out-of-sample window covers [IsEndEx, OosEndEx),
// so no candle date can belong to both windows and adjacent windows have no
// gap. The public backtest API takes inclusive end dates — convert with
// excl.AddDate(0, 0, -1) at the boundary (see foldDates).
type foldWindow struct {
	Index    int
	IsStart  time.Time // inclusive
	IsEndEx  time.Time // exclusive — also the OOS start
	OosEndEx time.Time // exclusive
}

// addMonthsUTC adds whole calendar months anchored at d: the day of month is
// preserved when it exists in the target month and clamped to its last day
// otherwise (Jan 31 + 1m → Feb 28/29, Aug 31 + 1m → Sep 30). Boundaries are
// UTC date-only; chained folds anchor on the ORIGINAL start date so clamping
// can never accumulate drift.
func addMonthsUTC(d time.Time, months int) time.Time {
	y, m, _ := d.Date()
	total := (int(m) - 1) + months
	if total < 0 {
		total = 0
	}
	day := d.Day()
	last := time.Date(y+total/12, time.Month(total%12+2), 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(y+total/12, time.Month(total%12+1), day, 0, 0, 0, 0, time.UTC)
}

// generateFolds rolls (isMonths, oosMonths) windows forward by stepMonths
// over [start, endExcl). A fold is emitted only while its whole OOS window
// fits inside the range — an incomplete tail fold is dropped, never
// shortened. OOS windows never overlap when step >= oos (enforced).
func generateFolds(start, endExcl time.Time, isMonths, oosMonths, stepMonths int) []foldWindow {
	var folds []foldWindow
	for i := 0; ; i++ {
		isStart := addMonthsUTC(start, i*stepMonths)
		isEndEx := addMonthsUTC(start, i*stepMonths+isMonths)
		oosEndEx := addMonthsUTC(start, i*stepMonths+isMonths+oosMonths)
		if oosEndEx.After(endExcl) {
			break
		}
		folds = append(folds, foldWindow{Index: i, IsStart: isStart, IsEndEx: isEndEx, OosEndEx: oosEndEx})
	}
	return folds
}

// foldDates converts a fold's half-open boundaries into the inclusive
// start/end dates the backtest API (and persisted fold rows) use: isEnd is
// the last in-sample date and oosStart is the first OOS date — the two
// windows share no candle date.
func foldDates(f foldWindow) (isStart, isEnd, oosStart, oosEnd time.Time) {
	return f.IsStart, f.IsEndEx.AddDate(0, 0, -1), f.IsEndEx, f.OosEndEx.AddDate(0, 0, -1)
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
	for i, l := range req.Legs {
		if seen[l.StrategyID] {
			return verr("duplicate strategy: " + l.StrategyID)
		}
		seen[l.StrategyID] = true
		// Graph legs take their node parameters from the immutable saved
		// revision — overrides are rejected, provenance is injected instead.
		p, err := graphLegParameters(l.StrategyID, l.Parameters)
		if err != nil {
			return verr(err.Error())
		}
		req.Legs[i].Parameters = p
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
	// The public range is inclusive; internally folds live in the half-open
	// interval [start, end+1day) so a fold's OOS end may equal the day after
	// the requested end without exceeding it.
	folds := generateFolds(start, end.AddDate(0, 0, 1), req.InSampleMonths, req.OosMonths, step)
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
	if err := s.checkHoldout(ctx, req.AssetID, req.Timeframe, req.StartDate, req.EndDate, ""); err != nil {
		return api.WalkForward{}, err
	}
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
		isS, isE, oosS, oosE := foldDates(f)
		for _, l := range req.Legs {
			out.Folds = append(out.Folds, api.WalkForwardFold{
				FoldIndex: f.Index, StrategyID: l.StrategyID,
				IsStart: isS.Format("2006-01-02"), IsEnd: isE.Format("2006-01-02"),
				OosStart: oosS.Format("2006-01-02"), OosEnd: oosE.Format("2006-01-02"),
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
	if err := s.checkHoldout(ctx, req.AssetID, req.Timeframe, req.StartDate, req.EndDate, ""); err != nil {
		return api.WalkForward{}, err
	}
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
			isS, isE, oosS, oosE := foldDates(f)
			for _, w := range [][2]time.Time{{isS, isE}, {oosS, oosE}} {
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
		// Don't leave the row permanently "pending" without a trace: try to
		// record the failure while the DB may still be reachable.
		msg := fmt.Sprintf("failed to start: %v", err)
		if ferr := s.repos.WalkForwards.SetExperimentStatus(ctx, expID, "failed", &msg); ferr != nil {
			s.log.Error("walk-forward start transition also failed", "experiment_id", expID, "error", ferr.Error())
		}
		return api.WalkForward{}, fmt.Errorf("start experiment: %w", err)
	}
	s.log.Info("walk-forward started", "experiment_id", expID, "folds", len(folds), "strategies", len(req.Legs))

	// Persist every fold boundary first so the experiment is inspectable
	// even while/in case it fails partway.
	foldIDs := map[string]string{} // "foldIdx|strategy" → fold row id
	for _, f := range folds {
		isS, isE, oosS, oosE := foldDates(f)
		for _, leg := range req.Legs {
			fid, err := s.repos.WalkForwards.CreateFold(ctx, repository.WalkForwardFoldRow{
				ExperimentID: expID, FoldIndex: f.Index, StrategyID: leg.StrategyID,
				IsStart: isS.Format("2006-01-02"), IsEnd: isE.Format("2006-01-02"),
				OosStart: oosS.Format("2006-01-02"), OosEnd: oosE.Format("2006-01-02"),
			})
			if err != nil {
				// A persisted experiment must not stay "running" forever with
				// pending folds: mark it and any already-created folds failed.
				msg := fmt.Sprintf("create fold %d %s: %v", f.Index, leg.StrategyID, err)
				s.failWalkForwardSetup(ctx, expID, foldIDs, msg)
				return api.WalkForward{}, fmt.Errorf("create fold %d %s: %w", f.Index, leg.StrategyID, err)
			}
			foldIDs[fmt.Sprintf("%d|%s", f.Index, leg.StrategyID)] = fid
		}
	}

	total, failed, persistErrs := 0, 0, 0
	var firstErr string
	for _, f := range folds {
		isS, isE, oosS, oosE := foldDates(f)
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
				{"in_sample", isS, isE, &isID},
				{"out_of_sample", oosS, oosE, &oosID},
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
			foldStatus := "completed"
			if foldErr != "" {
				foldStatus = "failed"
				failed++
				if firstErr == "" {
					firstErr = fmt.Sprintf("fold %d %s: %s", f.Index, leg.StrategyID, foldErr)
				}
			}
			var errCopy *string
			if foldErr != "" {
				errCopy = &foldErr
			}
			// A failed fold-result write must not leave the experiment looking
			// cleaner than it is — the fold would stay "pending" in the DB
			// while the summary claimed it ran.
			if err := s.repos.WalkForwards.SetFoldResult(ctx, fid, foldStatus, isID, oosID, errCopy); err != nil {
				persistErrs++
				s.log.Error("walk-forward fold result not persisted", "experiment_id", expID,
					"fold", f.Index, "strategy_id", leg.StrategyID, "error", err.Error())
			}
		}
	}

	status := "completed"
	var errMsg *string
	switch {
	case persistErrs > 0:
		// Distinguish DB persistence failures from strategy-run failures:
		// member runs exist, but the fold records don't reflect them, so the
		// experiment can never claim "completed".
		status = "partial_failed"
		if persistErrs == total {
			status = "failed"
		}
		msg := fmt.Sprintf("%d fold result(s) could not be persisted", persistErrs)
		if firstErr != "" {
			msg += "; first run error: " + firstErr
		}
		errMsg = &msg
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

// failWalkForwardSetup best-effort marks an experiment failed when setup
// broke after the experiment row was persisted, and marks every fold row
// already created as failed so none stays "pending" forever. Persistence
// failures here are logged, not propagated — the caller already has the
// original, more meaningful error.
func (s *Service) failWalkForwardSetup(ctx context.Context, expID string, foldIDs map[string]string, msg string) {
	for key, fid := range foldIDs {
		errCopy := "interrupted: " + msg
		if err := s.repos.WalkForwards.SetFoldResult(ctx, fid, "failed", nil, nil, &errCopy); err != nil {
			s.log.Error("walk-forward fold interruption not recorded", "experiment_id", expID,
				"fold_key", key, "error", err.Error())
		}
	}
	if err := s.repos.WalkForwards.SetExperimentStatus(ctx, expID, "failed", &msg); err != nil {
		s.log.Error("walk-forward failure transition not persisted", "experiment_id", expID, "error", err.Error())
	}
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
	exps := map[string][]float64{}
	tims := map[string][]float64{}
	for _, fr := range foldRows {
		f := api.WalkForwardFold{
			FoldIndex: fr.FoldIndex, StrategyID: fr.StrategyID,
			IsStart: fr.IsStart, IsEnd: fr.IsEnd, OosStart: fr.OosStart, OosEnd: fr.OosEnd,
			Status: fr.Status, Error: fr.Error,
		}
		// A fold-referenced run must be retrievable — a missing or unreadable
		// member run is a data-integrity problem, not an optional result.
		if fr.IsRunID != nil {
			res, err := s.comparisonResult(ctx, *fr.IsRunID)
			if err != nil {
				return api.WalkForward{}, fmt.Errorf("fold %d %s in-sample run %s: %w",
					fr.FoldIndex, fr.StrategyID, *fr.IsRunID, err)
			}
			f.IsResult = &res
		}
		if fr.OosRunID != nil {
			res, err := s.comparisonResult(ctx, *fr.OosRunID)
			if err != nil {
				return api.WalkForward{}, fmt.Errorf("fold %d %s out-of-sample run %s: %w",
					fr.FoldIndex, fr.StrategyID, *fr.OosRunID, err)
			}
			f.OosResult = &res
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
			// Fold risk aggregates — per-fold values collected, never pooled.
			if m.Expectancy != nil {
				exps[fr.StrategyID] = append(exps[fr.StrategyID], *m.Expectancy)
			}
			if m.TimeInMarketPct != nil {
				tims[fr.StrategyID] = append(tims[fr.StrategyID], *m.TimeInMarketPct)
			}
			if m.MaxConsecutiveLosses != nil &&
				(sum.WorstConsecutiveLosses == nil || *m.MaxConsecutiveLosses > *sum.WorstConsecutiveLosses) {
				v := *m.MaxConsecutiveLosses
				sum.WorstConsecutiveLosses = &v
			}
			if m.Expectancy == nil && m.AvgWin == nil && m.BarsInMarket == nil {
				sum.MissingMetricFolds++ // completed fold predating risk metrics
			}
		}
	}
	median := func(v []float64) *float64 {
		if len(v) == 0 {
			return nil
		}
		sort.Float64s(v)
		n := len(v)
		m := v[n/2]
		if n%2 == 0 {
			m = (v[n/2-1] + v[n/2]) / 2
		}
		return &m
	}
	for sid, sum := range sums {
		if v := median(rets[sid]); v != nil {
			sum.MedianReturnPct = *v
		}
		sum.MedianExpectancy = median(exps[sid])
		sum.MedianTimeInMarketPct = median(tims[sid])
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
		// Every completed run writes a metrics row; a completed run without
		// one means the run/metrics write was interrupted — report it rather
		// than returning a "successful" result with silently absent metrics.
		m, err := s.repos.Metrics.GetMetrics(ctx, r.ID)
		if err != nil {
			return api.ComparisonResult{}, fmt.Errorf("completed run metrics unreadable: %w", err)
		}
		if len(m) == 0 {
			return api.ComparisonResult{}, fmt.Errorf("completed run %s has no persisted metrics", r.ID)
		}
		mm := metricsDTO(m)
		res.Metrics = &mm
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
