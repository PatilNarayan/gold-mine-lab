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

// Commission and slippage adjust fills adversarially.
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
	wantPnl := (108.9 - 101) * qty
	if !almostEq(*tr.Pnl, wantPnl) {
		t.Fatalf("pnl %v, want %v", *tr.Pnl, wantPnl)
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
