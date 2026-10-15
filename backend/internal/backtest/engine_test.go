package backtest

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// scripted is a deterministic test strategy: emits sigs[i] at bar i via the
// streaming interface. Fulfills strategy.Strategy + strategy.Streamer.
type scripted struct{ sigs map[int]strategy.Action }

func (s *scripted) ID() string                           { return "scripted" }
func (s *scripted) Name() string                         { return "Scripted" }
func (s *scripted) Version() string                      { return "0.0.0" }
func (s *scripted) SupportedAssets() []string            { return []string{"xauusd"} }
func (s *scripted) Timeframe() market.Timeframe          { return market.Timeframe1h }
func (s *scripted) Parameters() []strategy.ParameterSpec { return nil }
func (s *scripted) Evaluate(_ context.Context, _ []market.Candle) (strategy.Signal, error) {
	return strategy.Signal{Action: strategy.ActionNeutral}, nil
}
func (s *scripted) NewStream() strategy.Stream { return &scriptedStream{s: s} }

type scriptedStream struct {
	s *scripted
	i int
}

func (st *scriptedStream) Next(c market.Candle) strategy.Signal {
	a := st.s.sigs[st.i]
	st.i++
	return strategy.Signal{StrategyID: "scripted", Action: a, Price: c.Close, Timestamp: c.Time}
}

// candles builds n hourly candles with the given opens; close = open.
func candles(opens []float64) []market.Candle {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]market.Candle, len(opens))
	for i, o := range opens {
		out[i] = market.Candle{Time: base.Add(time.Duration(i) * time.Hour), Open: o, High: o, Low: o, Close: o, Volume: 1}
	}
	return out
}

func almostEq(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func baseInput() Input {
	return Input{
		Strategy:       &scripted{},
		Candles:        candles([]float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}),
		WindowStart:    0,
		InitialCapital: 10000,
		PositionSize:   10,
		Commission:     0,
		Slippage:       0,
	}
}

// Signals fire at close of bar i and fill at open of bar i+1.
func TestEngineNextCandleOpenExecution(t *testing.T) {
	opens := []float64{100, 100, 100, 200, 200, 200, 200, 300, 300, 300}
	in := baseInput()
	in.Candles = candles(opens)
	in.Strategy = &scripted{sigs: map[int]strategy.Action{2: strategy.ActionBuy, 5: strategy.ActionSell}}

	res, err := NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(res.Trades))
	}
	tr := res.Trades[0]
	// BUY at bar 2 close → entry at bar 3 open = 200. SELL at bar 5 → exit at bar 6 open = 200.
	if !almostEq(tr.EntryPrice, 200) {
		t.Fatalf("entry %v, want 200 (next-candle open)", tr.EntryPrice)
	}
	if !almostEq(*tr.ExitPrice, 200) {
		t.Fatalf("exit %v, want 200", *tr.ExitPrice)
	}
	// EntryTime must equal candles[3].Time — the bar after the signal.
	want := in.Candles[3].Time
	if !tr.EntryTime.Equal(want) {
		t.Fatalf("entry time %v, want %v (next candle)", tr.EntryTime, want)
	}
}

// Commission and slippage adjust fills adversarially; P&L is net of both
// commissions while gross is preserved separately.
func TestEngineCosts(t *testing.T) {
	opens := []float64{100, 100, 100, 100, 100, 110, 110, 110, 110, 110}
	in := baseInput()
	in.Candles = candles(opens)
	in.Commission = 1 // 1%
	in.Slippage = 1   // 1%
	in.Strategy = &scripted{sigs: map[int]strategy.Action{0: strategy.ActionBuy, 7: strategy.ActionSell}}

	res, err := NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Trades[0]
	// Entry at bar1 open 100 * 1.01 = 101. Exit at bar8 open 110 * 0.99 = 108.9.
	if !almostEq(tr.EntryPrice, 101) {
		t.Fatalf("entry %v, want 101 (with 1%% slippage)", tr.EntryPrice)
	}
	if !almostEq(*tr.ExitPrice, 108.9) {
		t.Fatalf("exit %v, want 108.9", *tr.ExitPrice)
	}
	qty := 10000 * 0.10 / 101
	wantGross := (108.9 - 101) * qty
	wantNet := wantGross - tr.EntryCommission - tr.ExitCommission
	if !almostEq(*tr.GrossPnl, wantGross) {
		t.Fatalf("gross %v, want %v", *tr.GrossPnl, wantGross)
	}
	if !almostEq(*tr.Pnl, wantNet) {
		t.Fatalf("net pnl %v, want %v (gross - commissions)", *tr.Pnl, wantNet)
	}
	if tr.EntryCommission <= 0 || tr.ExitCommission <= 0 {
		t.Fatal("both commissions must be recorded")
	}
	if *tr.Pnl >= *tr.GrossPnl {
		t.Fatal("net must be strictly below gross when commissions > 0")
	}
}

// A trade that's gross-positive but net-negative must classify as a loss.
func TestEngineMarginFlipTrade(t *testing.T) {
	opens := []float64{100, 100, 100, 100.05, 100.05, 100.05, 100.05, 100.05}
	in := baseInput()
	in.Candles = candles(opens)
	in.Commission = 1 // 1% each way on ~1% position → costs exceed the 0.05 move
	in.Strategy = &scripted{sigs: map[int]strategy.Action{0: strategy.ActionBuy, 5: strategy.ActionSell}}
	res, err := NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Trades[0]
	if *tr.GrossPnl <= 0 {
		t.Fatalf("fixture broken: gross should be positive, got %v", *tr.GrossPnl)
	}
	if *tr.Pnl >= 0 || tr.Result != "loss" {
		t.Fatalf("gross win / net loss must classify 'loss': net=%v result=%s", *tr.Pnl, tr.Result)
	}
}

// Net P&L reconciles exactly: final equity = initial + Σ net trade pnl.
func TestEngineEquityReconciliation(t *testing.T) {
	opens := []float64{100, 100, 100, 105, 105, 105, 95, 95, 95, 95}
	in := baseInput()
	in.Candles = candles(opens)
	in.Commission = 0.5
	in.Slippage = 0.2
	in.Strategy = &scripted{sigs: map[int]strategy.Action{
		0: strategy.ActionBuy, 4: strategy.ActionSell, // trade 1
		5: strategy.ActionBuy, 8: strategy.ActionSell, // trade 2 (loss)
	}}
	res, err := NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var sumNet float64
	for _, tr := range res.Trades {
		sumNet += *tr.Pnl
	}
	last := res.Equity[len(res.Equity)-1]
	if !almostEq(last.Equity, in.InitialCapital+sumNet) {
		t.Fatalf("final equity %v != initial %v + Σnet %v", last.Equity, in.InitialCapital, sumNet)
	}
}

// Final liquidation costs show up in the terminal equity point.
func TestEngineFinalLiquidationCosts(t *testing.T) {
	in := baseInput()
	in.Commission = 1
	in.Slippage = 1
	in.Strategy = &scripted{sigs: map[int]strategy.Action{0: strategy.ActionBuy}}
	res, err := NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Trades[0]
	last := res.Equity[len(res.Equity)-1]
	if !almostEq(last.Equity, in.InitialCapital+*tr.Pnl) {
		t.Fatalf("terminal equity %v != initial + net %v", last.Equity, *tr.Pnl)
	}
	// With 1% slippage + 2% commissions the trade must be a net loss.
	if tr.Result != "loss" {
		t.Fatalf("expected cost-driven loss, got %s", tr.Result)
	}
}

// An open position at the last bar is force-closed at that bar's close.
func TestEngineForceCloseAtEnd(t *testing.T) {
	in := baseInput()
	in.Strategy = &scripted{sigs: map[int]strategy.Action{0: strategy.ActionBuy}}
	res, err := NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || res.Trades[0].Status != "closed" {
		t.Fatalf("expected 1 closed trade, got %+v", res.Trades)
	}
	last := in.Candles[len(in.Candles)-1]
	if !res.Trades[0].ExitTime.Equal(last.Time) {
		t.Fatal("position not closed at last bar")
	}
}

// BUY while long is ignored; SELL while flat is ignored (no shorts).
func TestEngineSinglePosition(t *testing.T) {
	in := baseInput()
	in.Candles = candles([]float64{100, 100, 100, 100, 100, 150, 150, 150, 150, 150})
	in.Strategy = &scripted{sigs: map[int]strategy.Action{
		0: strategy.ActionSell, // flat → ignored
		1: strategy.ActionBuy,  // fills at bar 2
		3: strategy.ActionBuy,  // already long → ignored
		4: strategy.ActionSell, // exits at bar 5
		6: strategy.ActionSell, // flat → ignored
	}}
	res, err := NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("want exactly 1 trade (no shorts/pyramids), got %d", len(res.Trades))
	}
	// Entry 100 (bar2 open), exit 150 (bar5 open) → profitable.
	if *res.Trades[0].Pnl <= 0 {
		t.Fatalf("expected winning trade, pnl %v", *res.Trades[0].Pnl)
	}
}

// Prove no look-ahead: a prefix-evaluating strategy asserts it never sees a
// candle newer than the one being evaluated.
func TestEngineNoLookAhead(t *testing.T) {
	rec := &lookaheadGuard{seenMax: -1}
	in := baseInput()
	in.Strategy = rec
	if _, err := NewEngine().Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if rec.violated {
		t.Fatal("strategy observed future candles")
	}
}

type lookaheadGuard struct {
	seenMax  int
	violated bool
}

func (g *lookaheadGuard) ID() string                           { return "guard" }
func (g *lookaheadGuard) Name() string                         { return "guard" }
func (g *lookaheadGuard) Version() string                      { return "0" }
func (g *lookaheadGuard) SupportedAssets() []string            { return nil }
func (g *lookaheadGuard) Timeframe() market.Timeframe          { return market.Timeframe1h }
func (g *lookaheadGuard) Parameters() []strategy.ParameterSpec { return nil }

func (g *lookaheadGuard) Evaluate(_ context.Context, candles []market.Candle) (strategy.Signal, error) {
	// Each prefix must be strictly longer than the last, and the engine must
	// never shrink or hand over the full series early.
	if len(candles) <= g.seenMax {
		g.violated = true
	}
	g.seenMax = len(candles)
	return strategy.Signal{Action: strategy.ActionNeutral}, nil
}

func TestEngineValidation(t *testing.T) {
	bad := baseInput()
	bad.InitialCapital = -1
	if _, err := NewEngine().Run(context.Background(), bad); err == nil {
		t.Fatal("negative capital must fail")
	}
	bad = baseInput()
	bad.PositionSize = 0
	if _, err := NewEngine().Run(context.Background(), bad); err == nil {
		t.Fatal("zero position size must fail")
	}
	bad = baseInput()
	bad.Candles[3].Time = bad.Candles[2].Time // unsorted
	if _, err := NewEngine().Run(context.Background(), bad); err == nil {
		t.Fatal("non-ascending candles must fail")
	}
}

// Warm-up bars feed indicators but never trade: a BUY emitted before
// WindowStart is dropped, and a BUY on the final window bar cannot fill
// because there is no later bar inside the window.
func TestEngineWarmupAndWindowEdges(t *testing.T) {
	in := baseInput()
	in.WindowStart = 4
	in.Strategy = &scripted{sigs: map[int]strategy.Action{
		3: strategy.ActionBuy, // last warm-up bar → must be ignored
		9: strategy.ActionBuy, // last bar → no next-open inside the window
	}}
	res, err := NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 0 {
		t.Fatalf("got %d trades, want 0 (warm-up/last-bar signals must not fill)", len(res.Trades))
	}
	if len(res.Equity) != 6 || !res.Equity[0].Time.Equal(in.Candles[4].Time) {
		t.Fatalf("equity must cover window bars only: len=%d first=%v", len(res.Equity), res.Equity[0].Time)
	}

	// A BUY on the first window bar fills at the next bar's open, in-window.
	in.Strategy = &scripted{sigs: map[int]strategy.Action{4: strategy.ActionBuy}}
	res, err = NewEngine().Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || !res.Trades[0].EntryTime.Equal(in.Candles[5].Time) {
		t.Fatalf("want 1 trade entering at bar 5, got %+v", res.Trades)
	}
}
