// Package metrics computes performance statistics from a backtest result.
//
// Formulas (all documented, none faked):
//
//	netPnl        = finalEquity - initialCapital
//	returnPct     = netPnl / initialCapital
//	winRate       = winningTrades / totalTrades        (closed trades only)
//	avgTradePnl   = netPnl / totalTrades
//	grossProfit   = Σ NET pnl over winning trades      (after commissions)
//	grossLoss     = |Σ NET pnl over losing trades|     (after commissions)
//	profitFactor  = grossProfit / grossLoss            (0 when no losses)
//	totalCommissions = Σ (entryCommission + exitCommission)
//
// "gross" in grossProfit/grossLoss is the conventional trade-report name for
// the winners'/losers' totals; both are NET of commissions (a trade's
// per-trade GrossPnl, before commissions, is a different quantity). Names
// are kept for compatibility with stored backtest_metrics rows.
//
//	maxDrawdown   = max over equity curve of (peak - equity) / peak
//
// Risk metrics (Phase 6a; NET trade P&L, deterministic, no DB):
//
//	avgWin         = Σ net winners / winningTrades        (null if none)
//	avgLoss        = |Σ net losers / losingTrades|        (positive magnitude; null if none)
//	payoffRatio    = avgWin / avgLoss                     (null if either side missing)
//	expectancy     = netPnl / totalTrades                 (= avgTradePnl; null if no closed trades)
//	maxConsLosses  = longest streak of net-loss trades in exit order (break-even resets)
//	barsInMarket / eligibleEvalBars / timeInMarketPct     (bar-based exposure, not wall-clock)
//	recoveryFactor = netPnl / maxDrawdownAmount           (null when max DD amount = 0)
//
// Undefined metrics are omitted from persistence — an absent row means
// "unavailable", never a fabricated value. Legacy runs simply lack the
// newer metric rows; the API reports them as null.
//
// Sharpe ratio is intentionally not produced: the engine's equity series is
// per-bar over a mixed-frequency set of runs, so annualization assumptions
// would be invented, not measured. Add it when a defined return frequency
// exists.
package metrics

import (
	"github.com/gold-mine-lab/backend/internal/backtest"
	"github.com/gold-mine-lab/backend/internal/repository"
)

// Summary is the computed metric set (names match backtest_metrics rows).
type Summary struct {
	NetPnl      float64
	ReturnPct   float64
	TotalTrades int
	Winning     int
	Losing      int
	WinRate     float64
	AvgTradePnl float64
	GrossProfit float64
	GrossLoss   float64
	// TotalCommissions is Σ(entryCommission + exitCommission) across all
	// trades — the total cost drag paid inside this run.
	TotalCommissions float64
	ProfitFactor     float64
	MaxDrawdown      float64
	// Risk metrics. Pointers are null when the metric is undefined (no
	// qualifying trades or a zero denominator); nil is persisted as an
	// absent metric row, never a fabricated number.
	AvgWin               *float64 // average NET P&L of winning trades
	AvgLoss              *float64 // average NET loss magnitude (positive)
	PayoffRatio          *float64 // avgWin / avgLoss
	Expectancy           *float64 // netPnl / closed trades
	RecoveryFactor       *float64 // netPnl / maxDD amount (null when DD = 0)
	TimeInMarketPct      *float64 // 100 * barsInMarket / eligibleEvalBars
	MaxConsecutiveLosses int
	BarsInMarket         int
	EligibleBars         int
}

// Names is the canonical metric_name vocabulary stored in backtest_metrics.
var Names = []string{
	"net_pnl", "return_pct", "total_trades", "winning_trades", "losing_trades",
	"win_rate", "avg_trade_pnl", "gross_profit", "gross_loss",
	"total_commissions", "profit_factor", "max_drawdown",
	"avg_win", "avg_loss", "payoff_ratio", "expectancy",
	"max_consecutive_losses", "bars_in_market", "eligible_eval_bars",
	"time_in_market_pct", "recovery_factor",
}

// Compute derives metrics from engine output. Deterministic.
func Compute(res backtest.Result, initialCapital float64) Summary {
	var s Summary
	final := res.Equity[len(res.Equity)-1].Equity

	s.NetPnl = final - initialCapital
	s.ReturnPct = s.NetPnl / initialCapital

	var sumWin, sumLoss float64
	var streak int
	for _, t := range res.Trades {
		if t.Status != "closed" || t.Pnl == nil {
			continue
		}
		s.TotalTrades++
		s.TotalCommissions += t.EntryCommission + t.ExitCommission
		switch {
		case *t.Pnl > 0:
			s.Winning++
			s.GrossProfit += *t.Pnl
			sumWin += *t.Pnl
			streak = 0
		case *t.Pnl < 0:
			s.Losing++
			s.GrossLoss -= *t.Pnl // stored as a positive magnitude
			sumLoss += *t.Pnl     // negative sum
			streak++
			if streak > s.MaxConsecutiveLosses {
				s.MaxConsecutiveLosses = streak
			}
		default:
			streak = 0 // break-even trade ends a losing streak
		}
	}
	if s.TotalTrades > 0 {
		s.WinRate = float64(s.Winning) / float64(s.TotalTrades)
		s.AvgTradePnl = s.NetPnl / float64(s.TotalTrades)
		e := s.AvgTradePnl // expectancy = average net P&L per closed trade
		s.Expectancy = &e
	}
	if s.Winning > 0 {
		v := sumWin / float64(s.Winning)
		s.AvgWin = &v
	}
	if s.Losing > 0 {
		v := -sumLoss / float64(s.Losing) // positive magnitude
		s.AvgLoss = &v
	}
	if s.AvgWin != nil && s.AvgLoss != nil {
		v := *s.AvgWin / *s.AvgLoss
		s.PayoffRatio = &v
	}
	if s.GrossLoss > 0 {
		s.ProfitFactor = s.GrossProfit / s.GrossLoss
	} else if s.GrossProfit > 0 {
		s.ProfitFactor = 0 // no losses — PF is undefined, not infinity
	}

	peak := res.Equity[0].Equity
	var maxDDAmount float64
	for _, p := range res.Equity {
		if p.Equity > peak {
			peak = p.Equity
		}
		if peak > 0 {
			if dd := (peak - p.Equity) / peak; dd > s.MaxDrawdown {
				s.MaxDrawdown = dd
			}
			if amt := peak - p.Equity; amt > maxDDAmount {
				maxDDAmount = amt
			}
		}
	}
	if maxDDAmount > 0 {
		v := s.NetPnl / maxDDAmount
		s.RecoveryFactor = &v
	}
	s.BarsInMarket = res.BarsInMarket
	s.EligibleBars = res.EligibleBars
	if res.EligibleBars > 0 {
		v := 100 * float64(res.BarsInMarket) / float64(res.EligibleBars)
		s.TimeInMarketPct = &v
	}
	return s
}

// Rows flattens the summary to (name, value) pairs for backtest_metrics.
func (s Summary) Rows() []repository.MetricValue {
	rows := []repository.MetricValue{
		{Name: "net_pnl", Value: s.NetPnl},
		{Name: "return_pct", Value: s.ReturnPct},
		{Name: "total_trades", Value: float64(s.TotalTrades)},
		{Name: "winning_trades", Value: float64(s.Winning)},
		{Name: "losing_trades", Value: float64(s.Losing)},
		{Name: "win_rate", Value: s.WinRate},
		{Name: "avg_trade_pnl", Value: s.AvgTradePnl},
		{Name: "gross_profit", Value: s.GrossProfit},
		{Name: "gross_loss", Value: s.GrossLoss},
		{Name: "total_commissions", Value: s.TotalCommissions},
		{Name: "profit_factor", Value: s.ProfitFactor},
		{Name: "max_drawdown", Value: s.MaxDrawdown},
		{Name: "max_consecutive_losses", Value: float64(s.MaxConsecutiveLosses)},
		{Name: "bars_in_market", Value: float64(s.BarsInMarket)},
		{Name: "eligible_eval_bars", Value: float64(s.EligibleBars)},
	}
	for _, row := range []struct {
		name string
		val  *float64
	}{
		{"avg_win", s.AvgWin},
		{"avg_loss", s.AvgLoss},
		{"payoff_ratio", s.PayoffRatio},
		{"expectancy", s.Expectancy},
		{"recovery_factor", s.RecoveryFactor},
		{"time_in_market_pct", s.TimeInMarketPct},
	} {
		if row.val != nil {
			rows = append(rows, repository.MetricValue{Name: row.name, Value: *row.val})
		}
	}
	return rows
}
