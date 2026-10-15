package eventbench

import (
	"math"
	"testing"
)

func mkObs(n int, eventEvery int, retOf func(i int) float64) []Observation {
	obs := make([]Observation, n)
	for i := range obs {
		obs[i] = Observation{Event: i%eventEvery == 0, Ret: retOf(i)}
	}
	return obs
}

func TestSummarizeCountsAndBaseline(t *testing.T) {
	// 100 bars, every 10th is an event. Events all +2, controls all -1.
	obs := mkObs(100, 10, func(i int) float64 {
		if i%10 == 0 {
			return 2
		}
		return -1
	})
	s := Summarize(obs, 6, Replicates, 42)
	if s.EventCount != 10 || s.ControlCount != 90 || s.BaselineCount != 100 {
		t.Fatalf("counts wrong: %+v", s)
	}
	if *s.EventMean != 2 || *s.BaselineMean != -0.7 {
		t.Fatalf("means wrong: ev=%v base=%v", *s.EventMean, *s.BaselineMean)
	}
	if s.DiffMean == nil || math.Abs(*s.DiffMean-2.7) > 1e-9 {
		t.Fatalf("diffMean wrong: %v", s.DiffMean)
	}
}

func TestInsufficientData(t *testing.T) {
	obs := mkObs(30, 10, func(i int) float64 { return 1 })
	s := Summarize(obs, 6, Replicates, 7)
	if s.CIStatus != StatusInsufficient {
		t.Fatalf("3 events must be insufficient_data, got %s", s.CIStatus)
	}
	if s.CILow != nil || s.CIHigh != nil {
		t.Fatal("no CI may be fabricated under minimums")
	}
	if s.EventCount != 3 || s.ControlCount != 27 {
		t.Fatal("descriptive counts must still be returned")
	}
}

func TestCIExcludesZeroWhenEventsStronglyHigher(t *testing.T) {
	// Events +3, controls −1, steady pattern → interval must exclude 0.
	obs := mkObs(400, 15, func(i int) float64 {
		if i%15 == 0 {
			return 3
		}
		return -1
	})
	s := Summarize(obs, 21, Replicates, 99)
	if s.CIStatus != StatusAvailable {
		t.Fatalf("expected available CI, got %s (valid=%d)", s.CIStatus, s.ValidReplicates)
	}
	if *s.CILow <= 0 {
		t.Fatalf("CI should exclude 0: [%v,%v]", *s.CILow, *s.CIHigh)
	}
}

func TestDeterminism(t *testing.T) {
	obs := mkObs(300, 12, func(i int) float64 {
		v := float64(i%7) - 3
		if i%12 == 0 {
			v += 2
		}
		return v
	})
	a := Summarize(obs, 11, Replicates, 5)
	b := Summarize(obs, 11, Replicates, 5)
	if a.CIStatus != b.CIStatus {
		t.Fatalf("nondeterministic status: %s vs %s", a.CIStatus, b.CIStatus)
	}
	if (a.CILow == nil) != (b.CILow == nil) ||
		(a.CILow != nil && (*a.CILow != *b.CILow || *a.CIHigh != *b.CIHigh)) {
		t.Fatal("identical inputs must produce identical intervals")
	}
}

func TestBlockLengthRespectsHorizon(t *testing.T) {
	obs := mkObs(200, 10, func(i int) float64 { return float64(i%5) - 2 })
	s := Summarize(obs, 21, Replicates, 3)
	if s.BlockLength != 21 {
		t.Fatalf("block length must be horizon+1, got %d", s.BlockLength)
	}
}
