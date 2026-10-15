package indicators

import (
	"math"
	"testing"
)

// Hand-computed Wilder RSI, period=3:
// prices: 100, 102, 104, 103, 105, 104, 106
// changes: +2, +2, -1, +2, -1, +2
// idx3 (seed): avgG=4/3, avgL=1/3  → RS=4  → RSI=80
// idx4: avgG=(4/3·2+2)/3=14/9, avgL=2/9 → RS=7 → RSI=87.5
// idx5: avgG=28/27, avgL=13/27 → RS=28/13 → RSI≈68.2927
// idx6: avgG=110/81, avgL=26/81 → RS=55/13 → RSI≈80.8824
func TestRSIKnownValues(t *testing.T) {
	prices := []float64{100, 102, 104, 103, 105, 104, 106}
	out := RSI(prices, 3)
	want := map[int]float64{
		3: 80.0,
		4: 87.5,
		5: 100 - 100/(1+28.0/13),
		6: 100 - 100/(1+55.0/13),
	}
	for i := 0; i < 3; i++ {
		if !math.IsNaN(out[i]) {
			t.Fatalf("RSI[%d] = %v, want NaN (undefined)", i, out[i])
		}
	}
	for i, w := range want {
		if math.Abs(out[i]-w) > 1e-9 {
			t.Fatalf("RSI[%d] = %v, want %v", i, out[i], w)
		}
	}
}

// Batch and streaming must produce identical values.
func TestRSIStreamEqualsBatch(t *testing.T) {
	prices := []float64{100, 101.2, 100.8, 102, 101.5, 103, 102.2, 104, 103.1,
		101.9, 102.8, 104.2, 103.6, 105, 104.4, 106.1, 105.3, 104.7, 105.9}
	batch := RSI(prices, 5)
	st := NewRSIState(5)
	for i, p := range prices {
		v, ok := st.Next(p)
		if math.IsNaN(batch[i]) {
			if ok {
				t.Fatalf("idx %d: batch NaN but stream defined %v", i, v)
			}
			continue
		}
		if !ok || math.Abs(v-batch[i]) > 1e-12 {
			t.Fatalf("idx %d: batch %v vs stream %v (ok=%v)", i, batch[i], v, ok)
		}
	}
}

func TestRSIEdgeCases(t *testing.T) {
	t.Run("constant prices → 50", func(t *testing.T) {
		out := RSI([]float64{42, 42, 42, 42, 42, 42}, 3)
		for i := 3; i < 6; i++ {
			if out[i] != 50 {
				t.Fatalf("flat series RSI[%d] = %v, want 50", i, out[i])
			}
		}
	})
	t.Run("all gains → 100", func(t *testing.T) {
		out := RSI([]float64{1, 2, 3, 4, 5, 6}, 3)
		if out[3] != 100 || out[5] != 100 {
			t.Fatalf("all-up series RSI = %v, want 100", out[3:])
		}
	})
	t.Run("all losses → 0", func(t *testing.T) {
		out := RSI([]float64{9, 8, 7, 6, 5, 4}, 3)
		if out[3] != 0 || out[5] != 0 {
			t.Fatalf("all-down series RSI = %v, want 0", out[3:])
		}
	})
	t.Run("NaN input propagates", func(t *testing.T) {
		out := RSI([]float64{1, 2, math.NaN(), 4, 5}, 2)
		for i := 2; i < 5; i++ {
			if !math.IsNaN(out[i]) {
				t.Fatalf("RSI[%d] = %v, want NaN after NaN input", i, out[i])
			}
		}
	})
	t.Run("insufficient history stays undefined", func(t *testing.T) {
		out := RSI([]float64{1, 2, 3}, 14)
		for i, v := range out {
			if !math.IsNaN(v) {
				t.Fatalf("RSI[%d] defined with only %d values", i, len(out))
			}
		}
	})
}

func TestRSIInvalidPeriod(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("period 0 must panic, like EMA")
		}
	}()
	RSI([]float64{1, 2, 3}, 0)
}
