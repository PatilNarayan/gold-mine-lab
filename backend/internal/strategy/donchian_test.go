package strategy

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
)

func donchianCandles(vals [][3]float64) []market.Candle {
	cs := make([]market.Candle, len(vals))
	for i, v := range vals {
		cs[i] = market.Candle{
			Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i),
			Open: v[0], High: v[0], Low: v[1], Close: v[2], Volume: 1,
		}
	}
	return cs
}

func TestDonchianBreakoutRules(t *testing.T) {
	s := NewDonchianBreakout()
	// 21 bars: first 20 highs=110/lows=100, close=105; bar 20 varies.
	base := make([][3]float64, 21)
	for i := 0; i < 20; i++ {
		base[i] = [3]float64{110, 100, 105}
	}

	buy := append([][3]float64{}, base...)
	buy[20] = [3]float64{115, 105, 115}
	sig, err := s.Evaluate(context.Background(), donchianCandles(buy))
	if err != nil || sig.Action != ActionBuy {
		t.Fatalf("expected BUY got %v %v", sig.Action, err)
	}
	if sig.Score <= 0 || sig.Score > 1 {
		t.Fatalf("score out of range: %v", sig.Score)
	}

	sell := append([][3]float64{}, base...)
	sell[20] = [3]float64{95, 95, 95}
	sig, _ = s.Evaluate(context.Background(), donchianCandles(sell))
	if sig.Action != ActionSell {
		t.Fatalf("expected SELL got %v", sig.Action)
	}

	none := append([][3]float64{}, base...)
	none[20] = [3]float64{108, 102, 105}
	sig, _ = s.Evaluate(context.Background(), donchianCandles(none))
	if sig.Action != ActionNeutral || sig.Reason != "no breakout" {
		t.Fatalf("expected NEUTRAL/no breakout got %v %q", sig.Action, sig.Reason)
	}
}

func TestDonchianCurrentCandleExcluded(t *testing.T) {
	s := NewDonchianBreakout()
	// The last candle has a huge high of 500; if its own high entered the
	// channel, close 105 could never buy AND close 400 could never sell.
	// With exclusion, upper=110: close 400 > 110 → BUY.
	vals := make([][3]float64, 21)
	for i := 0; i < 20; i++ {
		vals[i] = [3]float64{110, 100, 105}
	}
	vals[20] = [3]float64{500, 90, 400} // huge high AND low; close 400
	sig, err := s.Evaluate(context.Background(), donchianCandles(vals))
	if err != nil || sig.Action != ActionBuy {
		t.Fatalf("current candle contaminated channel: %v %v", sig.Action, err)
	}
	// upper must still be 110, lower 100
	if got := sig.Metadata["upper_channel"]; got != 110.0 {
		t.Fatalf("upper=%v want 110", got)
	}
	if got := sig.Metadata["lower_channel"]; got != 100.0 {
		t.Fatalf("lower=%v want 100", got)
	}
}

func TestDonchianPersistentBreakoutRepeats(t *testing.T) {
	s := NewDonchianBreakout()
	st := s.NewStream()
	// Flat channel then two consecutive closes above the rolling max —
	// both bars must BUY (no fabricated one-time crossing).
	for i := 0; i < 20; i++ {
		st.Next(market.Candle{High: 110, Low: 100, Close: 105, Time: time.Now().Add(time.Duration(i) * time.Hour)})
	}
	s1 := st.Next(market.Candle{High: 120, Low: 105, Close: 115, Time: time.Now().Add(20 * time.Hour)})
	s2 := st.Next(market.Candle{High: 125, Low: 110, Close: 122, Time: time.Now().Add(21 * time.Hour)})
	if s1.Action != ActionBuy || s2.Action != ActionBuy {
		t.Fatalf("persistent breakout should repeat BUY, got %v %v", s1.Action, s2.Action)
	}
}

func TestDonchianBatchStreamEquivalence(t *testing.T) {
	s := NewDonchianBreakout()
	vals := make([][3]float64, 60)
	for i := range vals {
		h := 100 + math.Sin(float64(i))*8 + float64(i)*0.3
		l := h - 5
		vals[i] = [3]float64{h, l, (h + l) / 2}
	}
	cs := donchianCandles(vals)
	st := s.NewStream()
	for i, c := range cs {
		sg := st.Next(c)
		if i < s.lookback {
			if sg.Action != ActionNeutral {
				t.Fatalf("warm-up bar %d got %v", i, sg.Action)
			}
			continue
		}
		want, err := s.Evaluate(context.Background(), cs[:i+1])
		if err != nil {
			t.Fatalf("batch eval bar %d: %v", i, err)
		}
		if sg.Action != want.Action || math.Abs(sg.Score-want.Score) > 1e-9 {
			t.Fatalf("bar %d: stream %v(%v) vs batch %v(%v)", i, sg.Action, sg.Score, want.Action, want.Score)
		}
	}
}

func TestDonchianStreamIsolation(t *testing.T) {
	s := NewDonchianBreakout()
	a, b := s.NewStream(), s.NewStream()
	flat := market.Candle{High: 110, Low: 100, Close: 105}
	for i := 0; i < 20; i++ {
		a.Next(flat)
	}
	b.Next(market.Candle{High: 999, Low: 1, Close: 500}) // big bar only in b
	// a's channel is unaffected by b's bar: close 115 > flat upper 110 → BUY.
	if sa := a.Next(market.Candle{High: 120, Low: 100, Close: 115}); sa.Action != ActionBuy {
		t.Fatalf("stream a contaminated: %v", sa.Action)
	}
	// b has only 2 bars total → still warm-up
	if got := b.Next(flat); got.Action != ActionNeutral || got.Reason != "warm-up" {
		t.Fatalf("stream b lost its own warm-up state: %v %q", got.Action, got.Reason)
	}
}

func TestDonchianParamBounds(t *testing.T) {
	s := NewDonchianBreakout()
	for _, p := range []Parameters{
		{"lookback_period": 4.0}, {"lookback_period": 201.0},
		{"lookback_period": 20.5}, {"lookback_period": "x"},
	} {
		if _, err := s.Configure(p); err == nil {
			t.Fatalf("expected rejection for %v", p)
		}
	}
	c, err := s.Configure(Parameters{"lookback_period": 5.0})
	if err != nil || c.(*DonchianBreakout).lookback != 5 {
		t.Fatalf("valid bound rejected: %v", err)
	}
}

func TestDonchianScoreEdgeCases(t *testing.T) {
	if got := donchianScore(0, 10); got != 0 {
		t.Fatalf("zero dist → %v", got)
	}
	if got := donchianScore(10, 0); got != 1 {
		t.Fatalf("zero width breakout → %v want 1", got)
	}
	if got := donchianScore(math.NaN(), 10); got != 0 {
		t.Fatalf("NaN dist → %v", got)
	}
	if got := donchianScore(10, math.Inf(1)); got != 0 {
		t.Fatalf("Inf width → %v", got)
	}
	s := NewDonchianBreakout()
	if _, err := s.Evaluate(context.Background(), donchianCandles(make([][3]float64, 10))); err == nil {
		t.Fatal("expected insufficient-data error")
	}
}
