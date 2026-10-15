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
}

// Names is the canonical metric_name vocabulary stored in backtest_metrics.
var Names = []string{
	"net_pnl", "return_pct", "total_trades", "winning_trades", "losing_trades",
	"win_rate", "avg_trade_pnl", "gross_profit", "gross_loss",
	"total_commissions", "profit_factor", "max_drawdown",
}

// Compute derives metrics from engine output. Deterministic.
func Compute(res backtest.Result, initialCapital float64) Summary {
	var s Summary
	final := res.Equity[len(res.Equity)-1].Equity

	s.NetPnl = final - initialCapital
	s.ReturnPct = s.NetPnl / initialCapital

	for _, t := range res.Trades {
		if t.Status != "closed" || t.Pnl == nil {
			continue
		}
		s.TotalTrades++
		s.TotalCommissions += t.EntryCommission + t.ExitCommission
		if *t.Pnl > 0 {
			s.Winning++
			s.GrossProfit += *t.Pnl
		} else {
			s.Losing++
			s.GrossLoss -= *t.Pnl // stored as a positive magnitude
		}
	}
	if s.TotalTrades > 0 {
		s.WinRate = float64(s.Winning) / float64(s.TotalTrades)
		s.AvgTradePnl = s.NetPnl / float64(s.TotalTrades)
	}
	if s.GrossLoss > 0 {
		s.ProfitFactor = s.GrossProfit / s.GrossLoss
	} else if s.GrossProfit > 0 {
		s.ProfitFactor = 0 // no losses — PF is undefined, not infinity
	}

	peak := res.Equity[0].Equity
	for _, p := range res.Equity {
		if p.Equity > peak {
			peak = p.Equity
		}
		if peak > 0 {
			if dd := (peak - p.Equity) / peak; dd > s.MaxDrawdown {
				s.MaxDrawdown = dd
			}
		}
	}
	return s
}

// Rows flattens the summary to (name, value) pairs for backtest_metrics.
func (s Summary) Rows() []repository.MetricValue {
	return []repository.MetricValue{
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
	}
}
