package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/backtest"
)

func eq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCompute(t *testing.T) {
	// Two closed trades: +1000 win, -400 loss. Capital 10k → 10.6k.
	// Equity path: 10k → 10.5k (dd peak→current: (10.6? use curve below)
	p1, p2 := 1000.0, -400.0
	exit := time.Now()
	res := backtest.Result{
		Trades: []backtest.Trade{
			{Status: "closed", Result: "win", Pnl: &p1},
			{Status: "closed", Result: "loss", Pnl: &p2},
		},
		Equity: []backtest.EquityPoint{
			{Equity: 10000},
			{Equity: 10500}, // peak
			{Equity: 10000}, // trough after peak → dd = 500/10500
			{Equity: 10600}, // new high at end
		},
	}
	s := Compute(res, 10000)

	if !eq(s.NetPnl, 600) {
		t.Errorf("netPnl %v, want 600", s.NetPnl)
	}
	if !eq(s.ReturnPct, 0.06) {
		t.Errorf("returnPct %v", s.ReturnPct)
	}
	if s.TotalTrades != 2 || s.Winning != 1 || s.Losing != 1 {
		t.Errorf("trades %d w%d l%d", s.TotalTrades, s.Winning, s.Losing)
	}
	if !eq(s.WinRate, 0.5) {
		t.Errorf("winRate %v", s.WinRate)
	}
	if !eq(s.AvgTradePnl, 300) {
		t.Errorf("avgTradePnl %v", s.AvgTradePnl)
	}
	if !eq(s.GrossProfit, 1000) || !eq(s.GrossLoss, 400) {
		t.Errorf("gross %v/%v", s.GrossProfit, s.GrossLoss)
	}
	if s.ProfitFactor == nil || !eq(*s.ProfitFactor, 2.5) {
		t.Errorf("profitFactor %v", s.ProfitFactor)
	}
	if !eq(s.MaxDrawdown, 500.0/10500.0) {
		t.Errorf("maxDD %v, want %v", s.MaxDrawdown, 500.0/10500.0)
	}
	_ = exit
}

func TestComputeNoTradesNoLosses(t *testing.T) {
	res := backtest.Result{
		Equity: []backtest.EquityPoint{{Equity: 10000}, {Equity: 10100}},
	}
	s := Compute(res, 10000)
	if s.TotalTrades != 0 || s.WinRate != 0 || s.ProfitFactor != nil {
		t.Errorf("empty run must produce zeros, got %+v", s)
	}
	if !eq(s.NetPnl, 100) {
		t.Errorf("netPnl %v", s.NetPnl)
	}
	if s.AvgWin != nil || s.AvgLoss != nil || s.PayoffRatio != nil ||
		s.Expectancy != nil || s.RecoveryFactor != nil || s.TimeInMarketPct != nil {
		t.Errorf("no-trade run must leave undefined metrics nil: %+v", s)
	}
	rows := s.Rows()
	names := map[string]bool{}
	for _, r := range rows {
		names[r.Name] = true
	}
	// Always-defined metrics are persisted; undefined ones are absent,
	// never fabricated. Every persisted name is in the vocabulary.
	for _, want := range []string{"net_pnl", "total_trades", "max_drawdown",
		"max_consecutive_losses", "bars_in_market", "eligible_eval_bars"} {
		if !names[want] {
			t.Errorf("missing required row %s", want)
		}
	}
	known := map[string]bool{}
	for _, n := range Names {
		known[n] = true
	}
	for _, r := range rows {
		if !known[r.Name] {
			t.Errorf("persisted unknown metric %s", r.Name)
		}
	}
}

func TestComputeRiskMetrics(t *testing.T) {
	// Trades: +1000, -400, -200, -100, +300, 0 (break-even), -50.
	// Wins 2 (avg 650), losses 4 (avg 187.5), expectancy = 550/7.
	// Longest losing streak = 3. NetPnl = 550; maxDD amount = 500
	// (peak 10500 → trough 10000) → recovery 1.1. 4 of 8 bars in market.
	p := func(v float64) *float64 { return &v }
	res := backtest.Result{
		Trades: []backtest.Trade{
			{Status: "closed", Result: "win", Pnl: p(1000)},
			{Status: "closed", Result: "loss", Pnl: p(-400)},
			{Status: "closed", Result: "loss", Pnl: p(-200)},
			{Status: "closed", Result: "loss", Pnl: p(-100)},
			{Status: "closed", Result: "win", Pnl: p(300)},
			{Status: "closed", Result: "win", Pnl: p(0)}, // break-even resets the streak
			{Status: "closed", Result: "loss", Pnl: p(-50)},
		},
		Equity: []backtest.EquityPoint{
			{Equity: 10000}, {Equity: 10500}, {Equity: 10000}, {Equity: 10550},
		},
		BarsInMarket: 4, EligibleBars: 8,
	}
	s := Compute(res, 10000)
	if s.AvgWin == nil || !eq(*s.AvgWin, 650) {
		t.Errorf("avgWin %v", s.AvgWin)
	}
	if s.AvgLoss == nil || !eq(*s.AvgLoss, 187.5) {
		t.Errorf("avgLoss %v", s.AvgLoss)
	}
	if s.PayoffRatio == nil || !eq(*s.PayoffRatio, 650.0/187.5) {
		t.Errorf("payoff %v", s.PayoffRatio)
	}
	if s.Expectancy == nil || !eq(*s.Expectancy, 550.0/7.0) {
		t.Errorf("expectancy %v", s.Expectancy)
	}
	if s.MaxConsecutiveLosses != 3 {
		t.Errorf("maxConsLosses %d, want 3", s.MaxConsecutiveLosses)
	}
	if s.TimeInMarketPct == nil || !eq(*s.TimeInMarketPct, 50) {
		t.Errorf("timeInMarket %v", s.TimeInMarketPct)
	}
	if s.RecoveryFactor == nil || !eq(*s.RecoveryFactor, 550.0/500.0) {
		t.Errorf("recovery %v", s.RecoveryFactor)
	}
	// Zero drawdown → recovery unavailable.
	flat := Compute(backtest.Result{
		Equity:       []backtest.EquityPoint{{Equity: 10000}, {Equity: 10100}},
		EligibleBars: 2,
		BarsInMarket: 0,
	}, 10000)
	if flat.RecoveryFactor != nil {
		t.Errorf("zero-DD recovery must be nil, got %v", *flat.RecoveryFactor)
	}
	// Winners only → payoff undefined (no loss side).
	win := Compute(backtest.Result{
		Trades: []backtest.Trade{{Status: "closed", Result: "win", Pnl: p(500)}},
		Equity: []backtest.EquityPoint{{Equity: 10000}, {Equity: 10500}},
	}, 10000)
	if win.PayoffRatio != nil || win.AvgLoss != nil || win.AvgWin == nil || win.ProfitFactor != nil {
		t.Errorf("winners-only payoff/PF must be nil: %+v", win)
	}
	// Losers only → PF is a valid 0 (zero profit over positive loss), not null.
	lose := Compute(backtest.Result{
		Trades: []backtest.Trade{{Status: "closed", Result: "loss", Pnl: p(-100)}},
		Equity: []backtest.EquityPoint{{Equity: 10000}, {Equity: 9900}},
	}, 10000)
	if lose.ProfitFactor == nil || !eq(*lose.ProfitFactor, 0) {
		t.Errorf("losers-only PF must be 0, got %v", lose.ProfitFactor)
	}
	// A net-loss trade is still a loss even if gross was positive —
	// classification lives on Pnl (net), so the streak counts it.
	if s.Losing != 4 {
		t.Errorf("losing %d, want 4", s.Losing)
	}
}
