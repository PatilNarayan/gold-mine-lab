package regime

import (
	"math"
	"testing"

	"github.com/gold-mine-lab/backend/internal/market"
)

func candles(n int, px func(i int) (h, l, c float64)) []market.Candle {
	out := make([]market.Candle, n)
	for i := range out {
		h, l, c := px(i)
		out[i] = market.Candle{Open: c, High: h, Low: l, Close: c}
	}
	return out
}

func TestTrendWarmupAndUptrend(t *testing.T) {
	cl := New()
	cs := candles(250, func(i int) (float64, float64, float64) {
		p := 100 + float64(i)
		return p + 1, p - 1, p
	})
	for i := 0; i < 199; i++ {
		if tr, _ := cl.Next(cs[i]); tr != TrendUnknown {
			t.Fatalf("bar %d: expected unknown during warm-up, got %s", i, tr)
		}
	}
	tr, _ := cl.Next(cs[199])
	if tr != TrendUp {
		t.Fatalf("bar 199 rising series: expected uptrend, got %s", tr)
	}
	tr, _ = cl.Next(cs[200])
	if tr != TrendUp {
		t.Fatalf("steady uptrend misclassified as %s", tr)
	}
}

func TestTrendDownAndTransition(t *testing.T) {
	cl := New()
	cs := candles(250, func(i int) (float64, float64, float64) {
		p := 400 - float64(i)
		return p + 1, p - 1, p
	})
	var last Trend
	for _, c := range cs {
		last, _ = cl.Next(c)
	}
	if last != TrendDown {
		t.Fatalf("steady decline: expected downtrend, got %s", last)
	}

	// Transition: long rise, sharp drop → close below EMA200 but EMA50 still
	// above EMA200 (neither up nor down definition holds).
	cl2 := New()
	for i := 0; i < 200; i++ {
		cl2.Next(market.Candle{Open: 100 + float64(i), High: 101 + float64(i), Low: 99 + float64(i), Close: 100 + float64(i)})
	}
	tr, _ := cl2.Next(market.Candle{Open: 200, High: 205, Low: 50, Close: 55})
	if tr != TrendTransition {
		t.Fatalf("expected transition after sharp drop below EMA200 with EMA50>EMA200, got %s", tr)
	}
}

func TestVolatilityWarmupAndLabels(t *testing.T) {
	cl := New()
	// 200 steady-range bars → ATR% stable → normal once baseline exists.
	steady := candles(200, func(i int) (float64, float64, float64) {
		p := 100 + 0.01*float64(i)
		return p + 1, p - 1, p
	})
	unknown := 0
	var last Volatility
	for _, c := range steady {
		_, last = cl.Next(c)
		if last == VolUnknown {
			unknown++
		}
	}
	// First classification needs 14 ATR + 100 baseline = 114 bars minimum.
	if unknown < 113 {
		t.Fatalf("volatility classified too early: only %d unknown bars", unknown)
	}
	if last != VolNormal {
		t.Fatalf("steady range: expected normal, got %s", last)
	}
	// A huge spike candle → high volatility.
	_, v := cl.Next(market.Candle{Open: 102, High: 150, Low: 60, Close: 100})
	if v != VolHigh {
		t.Fatalf("volatility spike: expected high, got %s", v)
	}
	// Many near-zero-range bars depress ATR% below the baseline → low.
	cl2 := New()
	for _, c := range steady {
		cl2.Next(c)
	}
	lowSeen := false
	for i := 0; i < 60; i++ {
		_, v = cl2.Next(market.Candle{Open: 102, High: 102.001, Low: 101.999, Close: 102})
		if v == VolLow {
			lowSeen = true
		}
	}
	if !lowSeen {
		t.Fatal("flat bars never classified as low volatility")
	}
}

func TestNoLookaheadAndDeterminism(t *testing.T) {
	mk := func() []market.Candle {
		return candles(220, func(i int) (float64, float64, float64) {
			p := 100 + 10*math.Sin(float64(i)/15) + 0.1*float64(i)
			return p + 1.5, p - 1.5, p
		})
	}
	cs := mk()
	clA, clB := New(), New()
	var la, lb Trend
	var va, vb Volatility
	for i := 0; i < 150; i++ {
		la, va = clA.Next(cs[i])
		lb, vb = clB.Next(cs[i])
	}
	// Diverge futures: A continues series, B gets wild candles — label
	// already emitted for bar 149 must be identical (no lookahead) and
	// deterministic across identical replays.
	for i := 150; i < 220; i++ {
		clA.Next(cs[i])
		clB.Next(market.Candle{Open: 999, High: 1000, Low: 990, Close: 995})
	}
	clC := New()
	for i := 0; i < 150; i++ {
		tc, vc := clC.Next(cs[i])
		if i == 149 && (tc != la || vc != va) {
			t.Fatalf("replay non-deterministic: %s/%s vs %s/%s", tc, vc, la, va)
		}
	}
	if lb != la || vb != va {
		t.Fatal("divergent future candles changed earlier labels")
	}
}
