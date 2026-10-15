// Package eventbench computes the statistical-validation layer for Signal
// Research (Phase 11): a matched historical baseline plus a moving-block
// bootstrap uncertainty interval for event-vs-control forward returns.
//
// It is a pure statistics component — no HTTP, PostgreSQL, market data, or
// strategy dependencies.
//
// Definitions:
//   - Event:   an eligible bar flagged as a BUY-episode start by the caller.
//   - Control: an eligible bar that is not an event.
//   - Baseline: ALL eligible bars (events + controls). Event observations are
//     a subset of the baseline, so descriptive differences against the
//     baseline are reported WITHOUT any independence assumption.
//   - Interval: 95% percentile moving-block bootstrap of
//     mean(event returns) − mean(control returns) over the aligned
//     chronological observation sequence. Block length = horizon + 1,
//     which preserves dependence inside each horizon window.
//
// The interval is an exploratory uncertainty estimate under this specific
// resampling scheme — not a formal significance test, and never proof of a
// profitable edge.
package eventbench

import (
	"math"
	"math/rand/v2"
	"sort"
)

const (
	// StudyVersion identifies the event/baseline conventions.
	StudyVersion = "eventstudy-v1"
	// BaselineVersion identifies the matched-baseline definition.
	BaselineVersion = "baseline-v1"
	// BootstrapVersion identifies the resampling method + seed scheme.
	BootstrapVersion = "mbb-v1"

	// Replicates is the default number of bootstrap replicates.
	Replicates = 1000
	// MinEvents / MinControls are operational minimums for a CI — not a
	// guarantee of statistical power.
	MinEvents    = 10
	MinControls  = 50
	MinValidRate = 0.8

	StatusAvailable    = "available"
	StatusInconclusive = "inconclusive"
	StatusInsufficient = "insufficient_data"
)

// Observation is one eligible evaluation bar, aligned chronologically.
type Observation struct {
	Event bool
	Ret   float64 // gross forward return % at this horizon
	Trend string  // regime label at this bar's close ("" when disabled)
	Vol   string
}

// Summary is the descriptive comparison + optional interval for one
// (candidate, horizon[, regime]) cell.
type Summary struct {
	EventCount       int
	ControlCount     int
	BaselineCount    int
	EventMean        *float64
	EventMedian      *float64
	BaselineMean     *float64
	BaselineMedian   *float64
	DiffMean         *float64 // event mean − baseline mean, percentage points
	DiffMedian       *float64
	EventPositivePct *float64
	BaselinePosPct   *float64
	WorstEventReturn *float64
	CIStatus         string
	CILow            *float64
	CIHigh           *float64
	ValidReplicates  int
	BlockLength      int
}

// Summarize computes descriptive stats and, when the minimums hold, the
// block-bootstrap interval. obs must be in chronological order.
func Summarize(obs []Observation, blockLen, reps int, seed uint64) Summary {
	s := Summary{BaselineCount: len(obs), CIStatus: StatusInsufficient, BlockLength: blockLen}
	var ev, ctl []float64
	for _, o := range obs {
		if o.Event {
			ev = append(ev, o.Ret)
		} else {
			ctl = append(ctl, o.Ret)
		}
	}
	s.EventCount, s.ControlCount = len(ev), len(ctl)
	if len(obs) > 0 {
		m, md := meanMed(obs, func(o Observation) float64 { return o.Ret })
		s.BaselineMean, s.BaselineMedian = &m, &md
		pp := pctPos(obs, func(o Observation) float64 { return o.Ret })
		s.BaselinePosPct = &pp
	}
	if len(ev) > 0 {
		m, md := meanMedF(ev)
		s.EventMean, s.EventMedian = &m, &md
		pp := pctPosF(ev)
		s.EventPositivePct = &pp
		w := minF(ev)
		s.WorstEventReturn = &w
		if len(obs) > 0 {
			d := m - *s.BaselineMean
			s.DiffMean = &d
			dm := md - *s.BaselineMedian
			s.DiffMedian = &dm
		}
	}
	if len(ev) < MinEvents || len(ctl) < MinControls {
		return s
	}
	low, high, valid, ok := blockBootstrap(obs, blockLen, reps, seed)
	s.ValidReplicates = valid
	if !ok {
		return s
	}
	s.CILow, s.CIHigh = &low, &high
	if low <= 0 && high >= 0 {
		s.CIStatus = StatusInconclusive
	} else {
		s.CIStatus = StatusAvailable
	}
	return s
}

// blockBootstrap resamples the chronological obs sequence in blocks of
// blockLen and returns the 2.5%/97.5% percentiles of
// meanEvent − meanControl across replicates. Prefix sums keep each
// replicate O(n/blockLen) with no copying. A replicate containing no
// events or no controls is skipped; too few valid replicates → ok=false.
func blockBootstrap(obs []Observation, blockLen, reps int, seed uint64) (float64, float64, int, bool) {
	n := len(obs)
	if blockLen < 1 {
		blockLen = 1
	}
	if blockLen > n {
		blockLen = n
	}
	// Prefix sums: evN/ctlN counts and evS/ctlS return sums.
	evN := make([]float64, n+1)
	ctlN := make([]float64, n+1)
	evS := make([]float64, n+1)
	ctlS := make([]float64, n+1)
	for i, o := range obs {
		evN[i+1], ctlN[i+1] = evN[i], ctlN[i]
		evS[i+1], ctlS[i+1] = evS[i], ctlS[i]
		if o.Event {
			evN[i+1]++
			evS[i+1] += o.Ret
		} else {
			ctlN[i+1]++
			ctlS[i+1] += o.Ret
		}
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	nBlocks := (n + blockLen - 1) / blockLen
	diffs := make([]float64, 0, reps)
	for r := 0; r < reps; r++ {
		var en, cn, es, cs float64
		for b := 0; b < nBlocks; b++ {
			s := rng.IntN(n)
			e := s + blockLen
			if e > n {
				e = n
			}
			en += evN[e] - evN[s]
			cn += ctlN[e] - ctlN[s]
			es += evS[e] - evS[s]
			cs += ctlS[e] - ctlS[s]
		}
		if en < 1 || cn < 1 {
			continue
		}
		diffs = append(diffs, es/en-cs/cn)
	}
	if float64(len(diffs)) < MinValidRate*float64(reps) || len(diffs) < 20 {
		return 0, 0, len(diffs), false
	}
	sort.Float64s(diffs)
	return percentile(diffs, 2.5), percentile(diffs, 97.5), len(diffs), true
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	i := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(i))
	hi := int(math.Ceil(i))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(i-float64(lo))
}

func meanMed[T any](xs []T, f func(T) float64) (float64, float64) {
	vs := make([]float64, len(xs))
	for i, x := range xs {
		vs[i] = f(x)
	}
	return meanMedF(vs)
}

func meanMedF(vs []float64) (float64, float64) {
	var sum float64
	for _, v := range vs {
		sum += v
	}
	s := append([]float64(nil), vs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return sum / float64(n), s[n/2]
	}
	return sum / float64(n), (s[n/2-1] + s[n/2]) / 2
}

func pctPos[T any](xs []T, f func(T) float64) float64 {
	pos := 0
	for _, x := range xs {
		if f(x) > 0 {
			pos++
		}
	}
	return float64(pos) / float64(len(xs)) * 100
}

func pctPosF(vs []float64) float64 {
	pos := 0
	for _, v := range vs {
		if v > 0 {
			pos++
		}
	}
	return float64(pos) / float64(len(vs)) * 100
}

func minF(vs []float64) float64 {
	m := vs[0]
	for _, v := range vs[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
