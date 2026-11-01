package tradeplan

import (
	"math"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

func candle(h, l, c float64) market.Candle {
	return market.Candle{Time: time.Now(), Open: c, High: h, Low: l, Close: c}
}

// flatCandles builds n candles with a constant true range of tr.
func flatCandles(n int, tr, mid float64) []market.Candle {
	cs := make([]market.Candle, n)
	for i := range cs {
		cs[i] = candle(mid+tr/2, mid-tr/2, mid)
	}
	return cs
}

// swingSeries builds candles whose LOW dips to `dip` exactly `strength` bars
// before the end, forming one confirmed swing low. ATR stays ~1.
func swingSeries(strength int, dip float64) []market.Candle {
	n := 20 + strength
	cs := flatCandles(n, 2, 100) // TR=2 everywhere
	cs[n-1-strength].Low = dip
	cs[n-1-strength].High = 101
	cs[n-1-strength].Close = 100.5
	return cs
}

func buySignal(id string, meta map[string]any) strategy.Signal {
	return strategy.Signal{
		StrategyID: id, Action: strategy.ActionBuy, Score: 0.8,
		Price: 100, Timestamp: time.Now(), Metadata: meta,
	}
}

func TestWilderATRKnownValue(t *testing.T) {
	cs := flatCandles(30, 2, 100) // every TR = 2
	if got := wilderATR(cs, 14); got != 2 {
		t.Fatalf("constant TR should give ATR=2, got %v", got)
	}
}

func TestWilderATRSmoothing(t *testing.T) {
	// First 14 TRs = 1 → seed ATR=1; one big TR=15 at the end:
	// ATR = (1*13 + 15) / 14 = 2.
	cs := flatCandles(16, 1, 100)
	cs[15].High = 114.5 // TR = 114.5-99.5 = 15
	if got := wilderATR(cs, 14); got != 2 {
		t.Fatalf("expected smoothed ATR=2, got %v", got)
	}
}

func TestSwingLowConfirmation(t *testing.T) {
	cs := swingSeries(3, 95)
	low, ok := latestSwingLow(cs, 3)
	if !ok || low != 95 {
		t.Fatalf("expected confirmed swing low 95, got %v ok=%v", low, ok)
	}
}

func TestUnconfirmedSwingIgnored(t *testing.T) {
	cs := swingSeries(3, 95)
	// A lower dip near the end stays UNCONFIRMED (fewer than `strength`
	// bars exist after it) — the older confirmed swing must still win.
	cs[len(cs)-2].Low = 97
	low, ok := latestSwingLow(cs, 3)
	if !ok || low != 95 {
		t.Fatalf("unconfirmed tail dip must not win; expected 95, got %v ok=%v", low, ok)
	}
}

func TestEMACrossStopAndTargets(t *testing.T) {
	cs := swingSeries(3, 95) // ATR≈2, buffer 0.5 → stop ≈ 94
	plan, rej := Calculate(Input{Signal: buySignal("ema-cross", nil), Entry: 100, Candles: cs}, DefaultConfig())
	if rej != nil {
		t.Fatalf("unexpected rejection: %v", rej)
	}
	if plan.Method != "swing-low-atr" || plan.StopAnchor != 95 {
		t.Fatalf("wrong anchor/method: %+v", plan)
	}
	if !(plan.StopLoss < 95 && plan.StopLoss > 93) {
		t.Fatalf("stop should sit just under the swing low, got %v", plan.StopLoss)
	}
	risk := plan.RiskPerUnit
	if plan.T1 != 100+risk || plan.T2 != 100+2*risk || plan.T3 != 100+3*risk {
		t.Fatalf("targets not at 1R/2R/3R: %+v", plan)
	}
	if !(plan.T1 < plan.T2 && plan.T2 < plan.T3) || plan.Direction != "LONG" {
		t.Fatalf("targets not strictly ascending: %+v", plan)
	}
}

func TestRSIStop(t *testing.T) {
	cs := swingSeries(3, 95)
	plan, rej := Calculate(Input{Signal: buySignal("rsi-mean-reversion", nil), Entry: 100, Candles: cs}, DefaultConfig())
	if rej != nil || plan.Method != "reversal-swing-atr" || plan.StopAnchor != 95 {
		t.Fatalf("rsi stop wrong: plan=%+v rej=%v", plan, rej)
	}
}

func TestDonchianStop(t *testing.T) {
	cs := flatCandles(30, 2, 100) // ATR=2 → buffer 0.5 → stop = 110-1 = 109
	sig := buySignal("donchian-breakout", map[string]any{"upper_channel": 110.0})
	plan, rej := Calculate(Input{Signal: sig, Entry: 112, Candles: cs}, DefaultConfig())
	if rej != nil {
		t.Fatalf("unexpected rejection: %v", rej)
	}
	if plan.Method != "channel-invalidation-atr" || plan.StopAnchor != 110 || plan.StopLoss != 109 {
		t.Fatalf("donchian stop wrong: %+v", plan)
	}
}

func TestDonchianMissingMetadata(t *testing.T) {
	cs := flatCandles(30, 2, 100)
	for _, meta := range []map[string]any{nil, {"upper_channel": "oops"}, {"upper_channel": math.NaN()}} {
		_, rej := Calculate(Input{Signal: buySignal("donchian-breakout", meta), Entry: 112, Candles: cs}, DefaultConfig())
		if rej == nil || rej.Reason != RejectMissingMetadata {
			t.Fatalf("meta=%v: expected missing_metadata, got %v", meta, rej)
		}
	}
}

func TestSellSignalNotAShortPlan(t *testing.T) {
	sig := buySignal("ema-cross", nil)
	sig.Action = strategy.ActionSell
	_, rej := Calculate(Input{Signal: sig, Entry: 100, Candles: swingSeries(3, 95)}, DefaultConfig())
	if rej == nil || rej.Reason != RejectNotActionable {
		t.Fatalf("SELL must not produce a plan, got %v", rej)
	}
}

func TestInsufficientHistory(t *testing.T) {
	_, rej := Calculate(Input{Signal: buySignal("ema-cross", nil), Entry: 100, Candles: flatCandles(10, 2, 100)}, DefaultConfig())
	if rej == nil || rej.Reason != RejectInsufficientHistory {
		t.Fatalf("expected insufficient_history, got %v", rej)
	}
}

func TestInvalidPrices(t *testing.T) {
	cs := swingSeries(3, 95)
	for _, entry := range []float64{math.NaN(), math.Inf(1), 0, -5} {
		_, rej := Calculate(Input{Signal: buySignal("ema-cross", nil), Entry: entry, Candles: cs}, DefaultConfig())
		if rej == nil || rej.Reason != RejectInvalidPrice {
			t.Fatalf("entry %v: expected invalid_price, got %v", entry, rej)
		}
	}
}

func TestStopOnWrongSide(t *testing.T) {
	cs := swingSeries(3, 95) // anchor 95 > entry → stop must reject
	_, rej := Calculate(Input{Signal: buySignal("ema-cross", nil), Entry: 90, Candles: cs}, DefaultConfig())
	if rej == nil || rej.Reason != RejectStopMisplaced {
		t.Fatalf("expected stop_misplaced, got %v", rej)
	}
}

func TestExcessiveRiskRejected(t *testing.T) {
	cs := swingSeries(3, 95) // stop≈94 → risk≈6 = 6% of 100 (under 10% cap: ok)
	cfg := DefaultConfig()
	cfg.MaxRiskFraction = 0.03 // tighten cap to 3% → reject
	_, rej := Calculate(Input{Signal: buySignal("ema-cross", nil), Entry: 100, Candles: cs}, cfg)
	if rej == nil || rej.Reason != RejectRiskOutOfLimits {
		t.Fatalf("expected risk_out_of_limits, got %v", rej)
	}
}

func TestUnknownStrategy(t *testing.T) {
	_, rej := Calculate(Input{Signal: buySignal("mystery", nil), Entry: 100, Candles: swingSeries(3, 95)}, DefaultConfig())
	if rej == nil || rej.Reason != RejectUnsupportedStrategy {
		t.Fatalf("expected unsupported_strategy, got %v", rej)
	}
}
