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
	if !eq(s.ProfitFactor, 2.5) {
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
	if s.TotalTrades != 0 || s.WinRate != 0 || s.ProfitFactor != 0 {
		t.Errorf("empty run must produce zeros, got %+v", s)
	}
	if !eq(s.NetPnl, 100) {
		t.Errorf("netPnl %v", s.NetPnl)
	}
	if len(s.Rows()) != len(Names) {
		t.Fatal("Rows must cover every metric name")
	}
}
