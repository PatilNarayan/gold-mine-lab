package indicators

import (
	"math"
	"testing"
)

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEMAConstantSeries(t *testing.T) {
	// EMA of a constant series is the constant.
	got := EMA([]float64{100, 100, 100, 100, 100}, 3)
	for i, v := range got {
		if !almost(v, 100) {
			t.Fatalf("ema[%d] = %v, want 100", i, v)
		}
	}
}

func TestEMAKnownValues(t *testing.T) {
	// Seed = first value; k = 2/(3+1) = 0.5
	// ema: 10 → (12*.5 + 10*.5)=11 → (14*.5+11*.5)=12.5 → (12*.5+12.5*.5)=12.25
	got := EMA([]float64{10, 12, 14, 12}, 3)
	want := []float64{10, 11, 12.5, 12.25}
	for i := range want {
		if !almost(got[i], want[i]) {
			t.Fatalf("ema[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEMAEmptyAndZero(t *testing.T) {
	if got := EMA(nil, 5); len(got) != 0 {
		t.Fatal("empty input must yield empty output")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("period 0 must panic")
		}
	}()
	EMA([]float64{1}, 0)
}

func TestEMAStateMatchesBatch(t *testing.T) {
	vals := []float64{5, 7, 3, 9, 11, 2, 6}
	batch := EMA(vals, 4)
	st := NewEMAState(4)
	for i, v := range vals {
		if got := st.Next(v); !almost(got, batch[i]) {
			t.Fatalf("stream ema[%d] = %v, want %v", i, got, batch[i])
		}
	}
}
