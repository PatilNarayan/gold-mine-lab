package demo

import (
	"sort"
	"sync"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
)

// Backtest runs, trades, metrics and equity curves are fully precomputed here
// — the deterministic counterpart of what a real backtest engine will persist.

type seedSpec struct {
	strategyID string
	assetID    string
	status     string
	startDate  string
	endDate    string
	daysAgo    int // offset into the historical window for createdAt
}

// XAUUSD-only seed runs (single-asset focus; one failed run for UI variety).
var seedSpecs = []seedSpec{
	{"ema-cross", "xauusd", "completed", "2024-02-01", "2024-11-30", 10},
	{"rsi-reversion", "xauusd", "completed", "2024-04-01", "2024-10-31", 6},
	{"atr-breakout", "xauusd", "completed", "2024-05-01", "2024-11-15", 3},
	{"rsi-reversion", "xauusd", "failed", "2024-06-01", "2024-12-01", 2},
	{"ema-cross", "xauusd", "completed", "2024-07-01", "2024-12-28", 1},
}

func defaultParameters(strategyID string) map[string]any {
	s, _ := strategyByID(strategyID)
	out := map[string]any{}
	for _, p := range s.Parameters {
		out[p.Name] = p.Default
	}
	return out
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func strPtr(s string) *string   { return &s }
func f64Ptr(v float64) *float64 { return &v }

// store is the in-memory run/detail registry behind BacktestService.
type store struct {
	mu       sync.Mutex
	runs     map[string]api.BacktestRun
	details  map[string]api.BacktestDetail
	userRuns int
	// seriesFn resolves candles for trade sampling (real CSV when configured).
	seriesFn seriesOf
}

func newStore() *store {
	s := &store{
		runs:     map[string]api.BacktestRun{},
		details:  map[string]api.BacktestDetail{},
		seriesFn: candleSeries,
	}
	for i, spec := range seedSpecs {
		strat, _ := strategyByID(spec.strategyID)
		created := historyEnd.Add(-time.Duration(spec.daysAgo) * 24 * time.Hour)
		run := api.BacktestRun{
			ID:             "run-" + pad3(i+1),
			StrategyID:     spec.strategyID,
			AssetID:        spec.assetID,
			Timeframe:      strat.Timeframe,
			StartDate:      spec.startDate,
			EndDate:        spec.endDate,
			InitialCapital: 100_000,
			PositionSize:   5,
			Commission:     0.02,
			Slippage:       0.05,
			Parameters:     defaultParameters(spec.strategyID),
			Status:         spec.status,
			CreatedAt:      iso(created),
		}
		if spec.status == "completed" {
			run.CompletedAt = strPtr(iso(created.Add(90 * time.Second)))
		}
		if spec.status == "failed" {
			run.Error = strPtr("Parameter sweep produced zero trades — check date range and thresholds")
		}
		s.runs[run.ID] = run
	}
	return s
}

func pad3(n int) string {
	s := "000" + intToStr(n)
	return s[len(s)-3:]
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// buildDetail produces a deterministic equity curve, trades, and metrics.
func (s *store) buildDetail(run api.BacktestRun) api.BacktestDetail {
	strat, found := strategyByID(run.StrategyID)
	m := meta(run.AssetID)
	detail := api.BacktestDetail{Run: run, Equity: []api.EquityPoint{}, Trades: []api.BacktestTrade{}}
	if found {
		stratCopy := strat
		detail.Strategy = &stratCopy
	}
	if run.Status == "failed" || !found {
		return detail
	}

	r := newRng("run|" + run.ID + "|" + run.StrategyID + "|" + run.AssetID)
	series, err := s.seriesFn(run.AssetID, run.Timeframe)
	if err != nil {
		return detail
	}
	candles := series.Candles

	// Equity curve: ~120 points between startDate and endDate.
	start, _ := time.Parse("2006-01-02", run.StartDate)
	end, _ := time.Parse("2006-01-02", run.EndDate)
	const points = 120
	step := end.Sub(start) / (points - 1)
	if step < time.Hour {
		step = time.Hour
	}
	drift := r.between(-0.0018, 0.0042)
	vol := r.between(0.012, 0.032)

	equity := make([]api.EquityPoint, 0, points)
	eq := run.InitialCapital
	peak := eq
	maxDD := 0.0
	for i := 0; i < points; i++ {
		if i > 0 {
			eq *= 1 + drift + (r.next()-0.5)*2*vol
		}
		peak = max(peak, eq)
		dd := round(eq/peak-1, 4)
		maxDD = min(maxDD, dd)
		equity = append(equity, api.EquityPoint{
			Time:     iso(start.Add(time.Duration(i) * step)),
			Equity:   round(eq, 2),
			Drawdown: dd,
		})
	}
	finalEquity := equity[len(equity)-1].Equity
	netPnl := round(finalEquity-run.InitialCapital, 2)

	// Trades sampled from the candle series.
	tradeCount := r.intBetween(16, 44)
	trades := make([]api.BacktestTrade, 0, tradeCount)
	for i := 0; i < tradeCount; i++ {
		entryIdx := r.intBetween(5, len(candles)-30)
		exitIdx := min(entryIdx+r.intBetween(2, 22), len(candles)-1)
		entry := candles[entryIdx]
		exit := candles[exitIdx]
		side := "BUY"
		if r.next() >= 0.55 {
			side = "SELL"
		}
		qty := round(r.between(0.25, 4), 2)
		direction := 1.0
		if side == "SELL" {
			direction = -1
		}
		cost := (run.Commission + run.Slippage) * 0.01 * entry.Close * qty
		open := r.next() < 0.07
		t := api.BacktestTrade{
			ID:         run.ID + "-t" + pad3(i+1),
			RunID:      run.ID,
			StrategyID: run.StrategyID,
			AssetID:    run.AssetID,
			Side:       side,
			EntryTime:  iso(entry.Time),
			EntryPrice: round(entry.Close, m.digits),
			Quantity:   qty,
		}
		if open {
			t.Status, t.Result = "open", "open"
		} else {
			pnl := round((exit.Close-entry.Close)*qty*direction-cost, 2)
			t.ExitTime = strPtr(iso(exit.Time))
			t.ExitPrice = f64Ptr(round(exit.Close, m.digits))
			t.Pnl = f64Ptr(pnl)
			t.Status = "closed"
			if pnl >= 0 {
				t.Result = "win"
			} else {
				t.Result = "loss"
			}
		}
		trades = append(trades, t)
	}

	// Metrics
	var wins, losses int
	var grossWin, grossLoss float64
	for _, t := range trades {
		if t.Status != "closed" || t.Pnl == nil {
			continue
		}
		if *t.Pnl > 0 {
			wins++
			grossWin += *t.Pnl
		} else {
			losses++
			grossLoss += -*t.Pnl
		}
	}
	closed := wins + losses
	var pf *float64
	if grossLoss > 0 {
		v := round(grossWin/grossLoss, 2)
		pf = &v
	} // no losers → PF undefined, not infinity
	winRate := 0.0
	avgTrade := 0.0
	if closed > 0 {
		winRate = round(float64(wins)/float64(closed), 4)
		avgTrade = round(netPnl/float64(closed), 2)
	}
	detail.Metrics = &api.BacktestMetrics{
		NetPnl:       netPnl,
		ReturnPct:    round(netPnl/run.InitialCapital, 4),
		WinRate:      winRate,
		ProfitFactor: pf,
		MaxDrawdown:  round(maxDD, 4),
		TotalTrades:  closed,
		AvgTradePnl:  avgTrade,
	}
	detail.Equity = equity
	detail.Trades = trades
	return detail
}

func (s *store) detail(runID string) (api.BacktestDetail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return api.BacktestDetail{}, false
	}
	d, ok := s.details[runID]
	if !ok {
		d = s.buildDetail(run)
		s.details[runID] = d
	}
	return d, true
}

func (s *store) listRuns(strategyID string) []api.BacktestRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]api.BacktestRun, 0, len(s.runs))
	for _, r := range s.runs {
		if strategyID == "" || r.StrategyID == strategyID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// execute creates and resolves a run synchronously (deterministic per id).
func (s *store) execute(req api.BacktestRequest) (api.BacktestRun, error) {
	_, found := strategyByID(req.StrategyID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userRuns++
	now := time.Now().UTC()
	run := api.BacktestRun{
		ID:             "run-user-" + intToStr(s.userRuns),
		StrategyID:     req.StrategyID,
		AssetID:        req.AssetID,
		Timeframe:      req.Timeframe,
		StartDate:      req.StartDate,
		EndDate:        req.EndDate,
		InitialCapital: req.InitialCapital,
		PositionSize:   req.PositionSize,
		Commission:     req.Commission,
		Slippage:       req.Slippage,
		Parameters:     req.Parameters,
		CreatedAt:      iso(now),
	}
	if !found {
		run.Status = "failed"
		run.Error = strPtr("Unknown strategy \"" + req.StrategyID + "\"")
	} else {
		run.Status = "completed"
		run.CompletedAt = strPtr(iso(now))
	}
	s.runs[run.ID] = run
	s.details[run.ID] = s.buildDetail(run)
	return run, nil
}

func (s *store) latestCompletedRunID() string {
	for _, r := range s.listRuns("") {
		if r.Status == "completed" {
			return r.ID
		}
	}
	return ""
}

func (s *store) allTrades() []api.BacktestTrade {
	var out []api.BacktestTrade
	for _, r := range s.listRuns("") {
		if r.Status != "completed" {
			continue
		}
		if d, ok := s.detail(r.ID); ok {
			out = append(out, d.Trades...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntryTime > out[j].EntryTime })
	return out
}

func (s *store) summary() api.DashboardSummary {
	var completed []api.BacktestDetail
	for _, r := range s.listRuns("") {
		if r.Status != "completed" {
			continue
		}
		if d, ok := s.detail(r.ID); ok && d.Metrics != nil {
			completed = append(completed, d)
		}
	}
	var netPnl, winRateSum, maxDD float64
	var best *api.BacktestDetail
	for _, d := range completed {
		m := d.Metrics
		netPnl += m.NetPnl
		winRateSum += m.WinRate
		maxDD = min(maxDD, m.MaxDrawdown)
		if best == nil || m.NetPnl > best.Metrics.NetPnl {
			copy := d
			best = &copy
		}
	}
	summary := api.DashboardSummary{
		TotalBacktests: len(completed),
		NetPnl:         round(netPnl, 2),
		MaxDrawdown:    round(maxDD, 4),
	}
	if len(completed) > 0 {
		summary.WinRate = round(winRateSum/float64(len(completed)), 4)
	}
	if best != nil {
		summary.LatestRunID = strPtr(best.Run.ID)
		summary.LatestRunStrategyID = strPtr(best.Run.StrategyID)
		if def, ok := strategyByID(best.Run.StrategyID); ok {
			summary.LatestRunStrategyName = strPtr(def.Name)
		}
	}

	// Aggregate equity curve: dedicated seeded series across the window.
	r := newRng("dashboard-equity")
	const points = 160
	step := historyEnd.Sub(historyStart) / (points - 1)
	eq := 100_000.0
	peak := eq
	summary.Equity = make([]api.EquityPoint, 0, points)
	for i := 0; i < points; i++ {
		if i > 0 {
			eq *= 1 + 0.0016 + (r.next()-0.5)*2*0.014
		}
		peak = max(peak, eq)
		summary.Equity = append(summary.Equity, api.EquityPoint{
			Time:     iso(historyStart.Add(time.Duration(i) * step)),
			Equity:   round(eq, 2),
			Drawdown: round(eq/peak-1, 4),
		})
	}
	return summary
}

func (s *store) performanceFor(strategyID string) api.StrategyPerformance {
	var perf api.StrategyPerformance
	var latest *api.BacktestRun
	for i, r := range s.listRuns(strategyID) {
		if r.Status != "completed" {
			continue
		}
		perf.TotalBacktests++
		if latest == nil { // listRuns is newest-first
			latest = &s.listRuns(strategyID)[i]
		}
	}
	if latest == nil {
		return perf
	}
	perf.LatestRunID = &latest.ID
	tf := string(latest.Timeframe)
	perf.LatestRunTimeframe = &tf
	perf.LatestRunStartDate = &latest.StartDate
	perf.LatestRunEndDate = &latest.EndDate
	if d, ok := s.detail(latest.ID); ok && d.Metrics != nil {
		perf.LatestRunNetPnl = d.Metrics.NetPnl
		perf.LatestRunWinRate = d.Metrics.WinRate
	}
	return perf
}
