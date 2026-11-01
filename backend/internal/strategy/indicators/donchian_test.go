package indicators

import (
	"math"
	"testing"
)

func TestDonchianChannelsSemantics(t *testing.T) {
	// 6 candles, lookback 3 — channel for bar i uses strictly [i-3, i-1].
	highs := []float64{10, 11, 12, 13, 14, 100} // bar5 spikes: must NOT enter its own channel
	lows := []float64{9, 10, 11, 12, 13, 99}
	u, l := DonchianChannels(highs, lows, 3)
	for i := 0; i < 3; i++ {
		if !math.IsNaN(u[i]) || !math.IsNaN(l[i]) {
			t.Fatalf("index %d must be undefined during warmup", i)
		}
	}
	if u[3] != 12 || l[3] != 9 {
		t.Fatalf("ch[3] should span bars0-2: u=%v l=%v", u[3], l[3])
	}
	if u[5] != 14 || l[5] != 11 {
		t.Fatalf("ch[5] must exclude bar5's own spike: u=%v l=%v", u[5], l[5])
	}
}

func TestDonchianChannelsIncrementalRecalc(t *testing.T) {
	// Appending a new bar recomputes only the tail — earlier values stable.
	h := []float64{10, 11, 12, 13, 14}
	l := []float64{9, 10, 11, 12, 13}
	u1, l1 := DonchianChannels(h, l, 2)
	u2, l2 := DonchianChannels(append(append([]float64{}, h...), 15), append(append([]float64{}, l...), 14), 2)
	for i := range u1 {
		same := (u1[i] == u2[i] && l1[i] == l2[i]) ||
			(math.IsNaN(u1[i]) && math.IsNaN(u2[i]) && math.IsNaN(l1[i]) && math.IsNaN(l2[i]))
		if !same {
			t.Fatalf("prefix values must be identical at %d", i)
		}
	}
	if u2[5] != 14 || l2[5] != 12 {
		t.Fatalf("ch[5] spans bars3-4: u=%v l=%v", u2[5], l2[5])
	}
}
