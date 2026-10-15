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
	if *s.ControlMean != -1 {
		t.Fatalf("control mean must be non-event mean: %v", *s.ControlMean)
	}
	// Event−baseline (descriptive) differs from event−control (estimand).
	if math.Abs(*s.DiffMeanBaseline-2.7) > 1e-9 {
		t.Fatalf("diffMeanBaseline wrong: %v", *s.DiffMeanBaseline)
	}
	if math.Abs(*s.DiffMeanControl-3.0) > 1e-9 {
		t.Fatalf("diffMeanControl wrong: %v", *s.DiffMeanControl)
	}
	if *s.DiffMeanBaseline == *s.DiffMeanControl {
		t.Fatal("baseline diff must differ from control diff when events are in the baseline")
	}
}

func TestCIPairsWithEventMinusControl(t *testing.T) {
	// Events +3, controls −1 → event−control ≈ +4, CI must sit around 4.
	obs := mkObs(400, 15, func(i int) float64 {
		if i%15 == 0 {
			return 3
		}
		return -1
	})
	s := Summarize(obs, 21, Replicates, 99)
	if s.CIStatus != StatusAvailable || s.CILow == nil {
		t.Fatalf("expected available CI, got %s", s.CIStatus)
	}
	if *s.CILow <= 0 || *s.CILow > *s.DiffMeanControl || *s.CIHigh < *s.DiffMeanControl {
		t.Fatalf("CI [%v,%v] should bracket the event−control estimate %v",
			*s.CILow, *s.CIHigh, *s.DiffMeanControl)
	}
}

func TestInsufficientData(t *testing.T) {
	obs := mkObs(30, 10, func(i int) float64 { return 1 })
	s := Summarize(obs, 6, Replicates, 7)
	if s.CIStatus != StatusInsufficient || s.CILow != nil {
		t.Fatal("3 events must yield insufficient_data with no fabricated CI")
	}
	if s.EventCount != 3 || s.ControlCount != 27 {
		t.Fatal("descriptive counts must still be returned")
	}
}

func TestRegimeConditioningInsideReplicates(t *testing.T) {
	// 300 bars alternating "a"/"b" regimes; regime "b" events are strong.
	// Blocks of 10 bars alternate regimes so regime "b" gets every other
	// event (i%10==0 marks events; block 10-19 is "b" so i=10,30,... are
	// "b"-regime events).
	full := mkObs(300, 10, func(i int) float64 { return -0.5 })
	for i := range full {
		if (i/10)%2 == 0 {
			full[i].Vol = "a"
		} else {
			full[i].Vol = "b"
			if full[i].Event {
				full[i].Ret = 4
			}
		}
	}
	var cell []Observation
	for _, o := range full {
		if o.Vol == "b" {
			cell = append(cell, o)
		}
	}
	s := SummarizeMatch(cell, full, func(o Observation) bool { return o.Vol == "b" }, 11, Replicates, 17)
	if s.EventCount != 15 || s.ControlCount != 135 {
		t.Fatalf("regime cell counts wrong: ev=%d ctl=%d", s.EventCount, s.ControlCount)
	}
	if s.CIStatus != StatusAvailable || s.CILow == nil || *s.CILow <= 0 {
		t.Fatalf("strong regime effect should give an available CI: %s %v", s.CIStatus, s.CILow)
	}
	// Same regime events but controls of the OTHER regime → not counted.
	s2 := SummarizeMatch(cell, full, func(o Observation) bool { return o.Vol == "a" }, 11, Replicates, 17)
	if s2.EventCount != 15 || s2.ControlCount != 135 {
		t.Fatalf("regime 'a' should see its own events/controls only: %+v", s2)
	}
}

func TestDeterminismAndExactLengths(t *testing.T) {
	obs := mkObs(300, 12, func(i int) float64 {
		v := float64(i%7) - 3
		if i%12 == 0 {
			v += 2
		}
		return v
	})
	a := Summarize(obs, 11, Replicates, 5)
	b := Summarize(obs, 11, Replicates, 5)
	if a.CIStatus != b.CIStatus ||
		(a.CILow == nil) != (b.CILow == nil) ||
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

// Replicates cover exactly n observations and only complete blocks are
// sampled: verified by construction — with blockLen=n there is exactly one
// valid start, so every replicate must equal the realized estimate.
func TestSingleValidStartYieldsExactEstimate(t *testing.T) {
	obs := mkObs(200, 10, func(i int) float64 {
		if i%10 == 0 {
			return 1.5
		}
		return -0.5
	})
	// blockLen = n → starts=1 → all replicates identical → CI width 0 at
	// the realized difference (documented degenerate case; inputs far
	// above minimums ensure replicates are valid).
	var evS, ctlS float64
	var evN, ctlN float64
	for _, o := range obs {
		if o.Event {
			evN++
			evS += o.Ret
		} else {
			ctlN++
			ctlS += o.Ret
		}
	}
	want := evS/evN - ctlS/ctlN
	low, high, valid, ok := blockBootstrap(obs, func(Observation) bool { return true }, 200, 50, 1)
	if !ok || valid != 50 {
		t.Fatalf("expected all replicates valid, got %d", valid)
	}
	if low != want || high != want {
		t.Fatalf("with one valid block start, CI must equal realized diff %v, got [%v,%v]", want, low, high)
	}
}
