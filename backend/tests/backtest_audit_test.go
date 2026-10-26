package tests

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/backtest"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/metrics"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/internal/strategy/indicators"
)

// ── Scripted strategy double ────────────────────────────────────────────

// scriptedStrategy emits a fixed action on chosen bar indices and RECORDS
// every candle slice it is shown — the causality probe.
type scriptedStrategy struct {
	actions map[int]strategy.Action
	seen    [][]market.Candle
}

func (s *scriptedStrategy) ID() string                  { return "audit-script" }
func (s *scriptedStrategy) Name() string                { return "Audit Script" }
func (s *scriptedStrategy) Version() string             { return "0.0.0" }
func (s *scriptedStrategy) SupportedAssets() []string   { return []string{"xauusd"} }
func (s *scriptedStrategy) Timeframe() market.Timeframe { return market.Timeframe1h }
func (s *scriptedStrategy) Parameters() []strategy.ParameterSpec {
	return nil
}
func (s *scriptedStrategy) Evaluate(_ context.Context, candles []market.Candle) (strategy.Signal, error) {
	cp := append([]market.Candle{}, candles...)
	s.seen = append(s.seen, cp)
	return strategy.Signal{StrategyID: s.ID(), Action: s.actions[len(candles)-1], Price: candles[len(candles)-1].Close}, nil
}

func barsAt(n int, open float64) []market.Candle {
	out := make([]market.Candle, n)
	for i := range out {
		out[i] = market.Candle{
			Time: time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour),
			Open: open, High: open, Low: open, Close: open,
		}
	}
	return out
}

func withOHLC(cs []market.Candle, i int, o, h, l, c float64) {
	cs[i].Open, cs[i].High, cs[i].Low, cs[i].Close = o, h, l, c
}

// ── 1. Look-ahead: signal at close(i) fills at open(i+1) ───────────────

// A BUY emitted on bar 3's close MUST fill at bar 4's open — not bar 3's
// close and not bar 4's close. Any earlier fill would be trading on
// information that didn't exist yet.
func TestAuditSignalFillsAtNextOpen(t *testing.T) {
	cs := barsAt(10, 100)
	withOHLC(cs, 3, 100, 105, 99, 104)  // signal bar: close=104
	withOHLC(cs, 4, 111, 118, 108, 115) // fill bar: open=111, close=115
	withOHLC(cs, 5, 90, 90, 88, 89)
	withOHLC(cs, 6, 130, 135, 129, 132) // exit signal bar
	withOHLC(cs, 7, 140, 141, 139, 140) // exit fill bar

	st := &scriptedStrategy{actions: map[int]strategy.Action{3: strategy.ActionBuy, 6: strategy.ActionSell}}
	res, err := backtest.NewEngine().Run(context.Background(), backtest.Input{
		Strategy: st, Candles: cs, WindowStart: 0,
		InitialCapital: 10000, PositionSize: 50, Commission: 0, Slippage: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("trades=%d want 1", len(res.Trades))
	}
	tr := res.Trades[0]
	if !tr.EntryTime.Equal(cs[4].Time) {
		t.Fatalf("entry at %v — expected bar-4 open %v (fill is next-bar open)", tr.EntryTime, cs[4].Time)
	}
	if tr.EntryPrice != 111 {
		t.Fatalf("entry price %v — expected open[4]=111, never the signal close 104", tr.EntryPrice)
	}
	if tr.ExitTime == nil || !tr.ExitTime.Equal(cs[7].Time) {
		t.Fatalf("exit at %v — expected bar-7 open", tr.ExitTime)
	}
	if *tr.ExitPrice != 140 {
		t.Fatalf("exit price %v — expected open[7]=140", *tr.ExitPrice)
	}
	// Accounting: qty=5000/111, gross=(140-111)*qty=29*45.045...
	wantQty := 5000.0 / 111
	if math.Abs(tr.Quantity-wantQty) > 1e-9 {
		t.Fatalf("qty %v want %v", tr.Quantity, wantQty)
	}
	wantNet := (140.0 - 111.0) * wantQty
	if math.Abs(*tr.Pnl-wantNet) > 1e-6 {
		t.Fatalf("net %v want %v", *tr.Pnl, wantNet)
	}
	final := res.Equity[len(res.Equity)-1].Equity
	if math.Abs(final-(10000+wantNet)) > 1e-6 {
		t.Fatalf("final equity %v, Σnet implies %v", final, 10000+wantNet)
	}
}

// ── 2. Causality: strategy never sees a future candle ──────────────────

func TestAuditStrategySeesOnlyPast(t *testing.T) {
	cs := barsAt(15, 100)
	st := &scriptedStrategy{actions: map[int]strategy.Action{}}
	_, err := backtest.NewEngine().Run(context.Background(), backtest.Input{
		Strategy: st, Candles: cs, WindowStart: 0,
		InitialCapital: 1000, PositionSize: 10, Commission: 0, Slippage: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.seen) != len(cs) {
		t.Fatalf("evaluated %d times over %d bars", len(st.seen), len(cs))
	}
	for i, seen := range st.seen {
		if len(seen) != i+1 {
			t.Fatalf("at bar %d strategy saw %d candles — the future leaked", i, len(seen))
		}
		if !seen[len(seen)-1].Time.Equal(cs[i].Time) {
			t.Fatalf("at bar %d the last visible candle is %v — not this bar", i, seen[len(seen)-1].Time)
		}
	}
}

// ── 3. Accounting reconciliation: commissions, slippage, equity ────────

func TestAuditAccountingReconciles(t *testing.T) {
	cs := barsAt(8, 100)
	withOHLC(cs, 1, 200, 200, 200, 200) // entry fill bar: open 200
	withOHLC(cs, 5, 300, 300, 300, 300) // exit fill bar: open 300
	st := &scriptedStrategy{actions: map[int]strategy.Action{0: strategy.ActionBuy, 4: strategy.ActionSell}}

	// slip 2% adverse: entry 200*1.02=204, exit 300*0.98=294
	// comm 1% of notional each side.
	res, err := backtest.NewEngine().Run(context.Background(), backtest.Input{
		Strategy: st, Candles: cs, WindowStart: 0,
		InitialCapital: 10000, PositionSize: 100, Commission: 1, Slippage: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Trades[0]
	qty := 10000.0 * 1.0 / 204 // all-in sizing
	entryComm := qty * 204 * 0.01
	exitComm := qty * 294 * 0.01
	gross := (294.0 - 204.0) * qty
	net := gross - entryComm - exitComm

	if math.Abs(tr.EntryPrice-204) > 1e-9 || math.Abs(*tr.ExitPrice-294) > 1e-9 {
		t.Fatalf("fills %v→%v, want 204→294", tr.EntryPrice, *tr.ExitPrice)
	}
	if math.Abs(tr.EntryCommission-entryComm) > 1e-6 || math.Abs(tr.ExitCommission-exitComm) > 1e-6 {
		t.Fatalf("commissions %v/%v want %v/%v", tr.EntryCommission, tr.ExitCommission, entryComm, exitComm)
	}
	if math.Abs(*tr.Pnl-net) > 1e-6 {
		t.Fatalf("net %v want %v", *tr.Pnl, net)
	}
	// Equity curve must reconcile: last point = initial + Σnet exactly.
	final := res.Equity[len(res.Equity)-1].Equity
	if math.Abs(final-(10000+net)) > 1e-6 {
		t.Fatalf("equity %v want %v", final, 10000+net)
	}
	// Metrics must reconcile with the same figures.
	m := metrics.Compute(res, 10000)
	if math.Abs(m.NetPnl-net) > 1e-6 {
		t.Fatalf("netPnl %v want %v", m.NetPnl, net)
	}
	if math.Abs(m.TotalCommissions-(entryComm+exitComm)) > 1e-6 {
		t.Fatalf("commissions %v want %v", m.TotalCommissions, entryComm+exitComm)
	}
	if m.TotalTrades != 1 || m.Winning != 1 || m.WinRate != 1.0 {
		t.Fatalf("trade counts wrong: %+v", m)
	}
	// Mark-to-market drawdown: while long at open-200 world, mid-run bars are
	// flat at 200 → mtm dips below peak at the 89-close tail bars? Peak path:
	// after entry at 204, closes stay ~200-ish until exit — verify maxDD is
	// the observed worst (peak→trough over the actual curve).
	var peak, maxDD float64
	peak = res.Equity[0].Equity
	for _, p := range res.Equity {
		if p.Equity > peak {
			peak = p.Equity
		}
		if dd := (peak - p.Equity) / peak; dd > maxDD {
			maxDD = dd
		}
	}
	if math.Abs(m.MaxDrawdown-maxDD) > 1e-12 {
		t.Fatalf("maxDD metric %v vs independent %v", m.MaxDrawdown, maxDD)
	}
}

// ── 4. End-of-window liquidation at final close ─────────────────────────

func TestAuditOpenPositionLiquidatedAtFinalClose(t *testing.T) {
	cs := barsAt(6, 100)
	withOHLC(cs, 1, 100, 100, 100, 100)
	withOHLC(cs, 5, 100, 100, 100, 150) // last bar closes at 150
	st := &scriptedStrategy{actions: map[int]strategy.Action{0: strategy.ActionBuy}}
	res, err := backtest.NewEngine().Run(context.Background(), backtest.Input{
		Strategy: st, Candles: cs, WindowStart: 0,
		InitialCapital: 1000, PositionSize: 100, Commission: 0, Slippage: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Trades[0]
	if tr.Status != "closed" || tr.ExitTime == nil || !tr.ExitTime.Equal(cs[5].Time) {
		t.Fatalf("open position not liquidated at final bar: %+v", tr)
	}
	if *tr.ExitPrice != 150 {
		t.Fatalf("liquidation price %v want final close 150", *tr.ExitPrice)
	}
	final := res.Equity[len(res.Equity)-1].Equity
	if math.Abs(final-(1000+*tr.Pnl)) > 1e-9 {
		t.Fatalf("terminal equity %v doesn't reconcile with net %v", final, *tr.Pnl)
	}
}

// ── 5. Metrics edge cases ───────────────────────────────────────────────

func TestAuditMetricsEdges(t *testing.T) {
	run := func(actions map[int]strategy.Action, cs []market.Candle) backtest.Result {
		res, err := backtest.NewEngine().Run(context.Background(), backtest.Input{
			Strategy: &scriptedStrategy{actions: actions}, Candles: cs, WindowStart: 0,
			InitialCapital: 1000, PositionSize: 100, Commission: 0, Slippage: 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	// Zero trades: no signals → flat equity, all rates defined-safely.
	flat := barsAt(6, 100)
	m := metrics.Compute(run(map[int]strategy.Action{}, flat), 1000)
	if m.TotalTrades != 0 || m.NetPnl != 0 || m.WinRate != 0 {
		t.Fatalf("zero-trade metrics wrong: %+v", m)
	}
	if m.ProfitFactor != nil || m.Expectancy != nil || m.AvgWin != nil || m.AvgLoss != nil {
		t.Fatal("undefined metrics must be nil, not fabricated")
	}

	// One loser: net<0 → loss; PF = 0/loss = 0 defined; avgWin nil.
	down := barsAt(6, 100)
	withOHLC(down, 1, 100, 100, 100, 100)
	withOHLC(down, 5, 50, 50, 50, 50) // liquidation close halves equity
	m = metrics.Compute(run(map[int]strategy.Action{0: strategy.ActionBuy}, down), 1000)
	if m.TotalTrades != 1 || m.Losing != 1 || m.WinRate != 0 {
		t.Fatalf("losing-trade metrics: %+v", m)
	}
	if m.ProfitFactor == nil || *m.ProfitFactor != 0 {
		t.Fatal("PF with losses and no wins should be 0, defined")
	}
	if m.AvgWin != nil {
		t.Fatal("avgWin must be nil with no winners")
	}

	// Break-even (net == 0): counted in totalTrades, in neither win nor loss —
	// and NOT classified 'win'.
	flat2 := barsAt(6, 100)
	res := run(map[int]strategy.Action{0: strategy.ActionBuy, 3: strategy.ActionSell}, flat2)
	m = metrics.Compute(res, 1000)
	if m.TotalTrades != 1 || m.Winning != 0 || m.Losing != 0 || m.WinRate != 0 {
		t.Fatalf("break-even metrics: %+v", m)
	}
	if res.Trades[0].Result != "breakeven" {
		t.Fatalf("break-even Result=%q — must be 'breakeven', not 'loss'", res.Trades[0].Result)
	}
}

// ── 6. Determinism: identical inputs → identical outputs ───────────────

func TestAuditDeterministic(t *testing.T) {
	// Alternating crosses produce several trades over a trending wave.
	cs := make([]market.Candle, 80)
	base := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := range cs {
		p := 100 + 20*math.Sin(float64(i)/4)
		cs[i] = market.Candle{Time: base.Add(time.Duration(i) * time.Hour),
			Open: p, High: p + 1, Low: p - 1, Close: p + 0.5}
	}
	run := func() backtest.Result {
		res, err := backtest.NewEngine().Run(context.Background(), backtest.Input{
			Strategy: strategy.NewEMACross(), Candles: cs, WindowStart: 0,
			InitialCapital: 50000, PositionSize: 25, Commission: 0.05, Slippage: 0.02,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	a, b := run(), run()
	if len(a.Trades) != len(b.Trades) || len(a.Equity) != len(b.Equity) {
		t.Fatal("runs diverged")
	}
	cmpF := func(x, y *float64) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return *x == *y
	}
	cmpT := func(x, y *time.Time) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return x.Equal(*y)
	}
	for i := range a.Trades {
		x, y := a.Trades[i], b.Trades[i]
		if !x.EntryTime.Equal(y.EntryTime) || x.EntryPrice != y.EntryPrice ||
			!cmpT(x.ExitTime, y.ExitTime) || !cmpF(x.ExitPrice, y.ExitPrice) ||
			x.Quantity != y.Quantity || !cmpF(x.GrossPnl, y.GrossPnl) || !cmpF(x.Pnl, y.Pnl) ||
			x.EntryCommission != y.EntryCommission || x.ExitCommission != y.ExitCommission ||
			x.Status != y.Status || x.Result != y.Result {
			t.Fatalf("trade %d differs: %+v vs %+v", i, x, y)
		}
	}
	for i := range a.Equity {
		if a.Equity[i] != b.Equity[i] {
			t.Fatalf("equity %d differs", i)
		}
	}
}

// ── 7. EMA against an independent reference implementation ──────────────

func refEMA(vals []float64, n int) []float64 {
	// Naive independent recompute — deliberately written differently from
	// the package under test.
	out := make([]float64, len(vals))
	k := 2.0 / (float64(n) + 1)
	for i := range vals {
		if i == 0 {
			out[i] = vals[0]
			continue
		}
		out[i] = vals[i]*k + out[i-1]*(1-k)
	}
	return out
}

func TestAuditEMAMatchesReference(t *testing.T) {
	vals := make([]float64, 200)
	for i := range vals {
		vals[i] = 2000 + 50*math.Sin(float64(i)/3) + float64(i%7)*1.3
	}
	for _, period := range []int{2, 5, 20, 50} {
		want := refEMA(vals, period)
		got := indicators.EMA(vals, period)
		if len(got) != len(vals) {
			t.Fatalf("EMA len %d", len(got))
		}
		for i := range want {
			if math.Abs(got[i]-want[i]) > 1e-12 {
				t.Fatalf("EMA%d[%d]=%v want %v", period, i, got[i], want[i])
			}
		}
		// Streaming state must produce the identical series.
		st := indicators.NewEMAState(period)
		for i, v := range vals {
			if g := st.Next(v); math.Abs(g-want[i]) > 1e-12 {
				t.Fatalf("stream EMA%d[%d]=%v want %v", period, i, g, want[i])
			}
		}
	}
}

// ── 8. Stream vs batch Evaluate parity + warmup gate ────────────────────

func TestAuditStreamBatchParity(t *testing.T) {
	cs := make([]market.Candle, 120)
	base := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := range cs {
		p := 2100 + 30*math.Sin(float64(i)/5)
		cs[i] = market.Candle{Time: base.Add(time.Duration(i) * time.Hour),
			Open: p, Close: p}
	}
	ec := strategy.NewEMACross()
	st := ec.NewStream()
	for i, c := range cs {
		streamSig := st.Next(c)
		batchSig, err := ec.Evaluate(context.Background(), cs[:i+1])
		if err != nil {
			// Batch refuses before slow+2 bars — stream must also be neutral
			// there (its gate is one bar earlier but never emits crosses
			// inside warmup that batch would act on differently).
			if streamSig.Action != strategy.ActionNeutral {
				t.Fatalf("bar %d: batch refuses but stream emitted %v", i, streamSig.Action)
			}
			continue
		}
		if streamSig.Action != batchSig.Action {
			t.Fatalf("bar %d: stream=%v batch=%v — evaluators diverge", i, streamSig.Action, batchSig.Action)
		}
	}
}
