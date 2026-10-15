// Package eventbench computes the statistical-validation layer for Signal
// Research (Phase 11/11.1): a matched historical baseline plus a
// moving-block bootstrap uncertainty interval for event-vs-control forward
// returns.
//
// It is a pure statistics component — no HTTP, PostgreSQL, market data, or
// strategy dependencies.
//
// Definitions:
//   - Event:   an eligible bar flagged as a BUY-episode start by the caller.
//   - Control: an eligible bar that is not an event.
//   - Baseline: ALL eligible bars (events + controls). Event observations are
//     a subset of the baseline, so event-vs-baseline differences are
//     descriptive only — never an independence claim.
//   - Point estimate paired with the interval: mean(event) − mean(control).
//   - Interval: 95% percentile moving-block bootstrap (mbb-v2) of that
//     estimate. Each replicate concatenates complete blocks sampled from
//     valid contiguous start positions until it covers the original
//     observation count, truncating only the final block — every replicate
//     has exactly n observations.
//   - Regime conditioning happens INSIDE each replicate: blocks are sampled
//     from the full chronological sequence and only observations whose own
//     label matches the regime contribute — preserving true time adjacency.
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
	// BootstrapVersion identifies the resampling method. v2 fixes block
	// sampling to complete blocks only (single truncation point) and moves
	// regime conditioning inside each replicate.
	BootstrapVersion = "mbb-v2"

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
// (candidate, horizon[, regime]) cell. Both differences are exposed
// separately: event−baseline is descriptive context; event−control is the
// point estimate the CI describes.
type Summary struct {
	EventCount     int
	ControlCount   int
	BaselineCount  int
	EventMean      *float64
	EventMedian    *float64
	BaselineMean   *float64
	BaselineMedian *float64
	// Control stats are non-event bars only (for the overall cell,
	// Control* == the complement of events inside the baseline).
	ControlMean   *float64
	ControlMedian *float64
	// Diff*Baseline: event − all-bar baseline (descriptive only).
	DiffMeanBaseline   *float64
	DiffMedianBaseline *float64
	// Diff*Control: event − non-event controls — the estimate the CI
	// describes.
	DiffMeanControl   *float64
	DiffMedianControl *float64
	EventPositivePct  *float64
	BaselinePosPct    *float64
	WorstEventReturn  *float64
	CIStatus          string
	CILow             *float64
	CIHigh            *float64
	ValidReplicates   int
	BlockLength       int
}

// Summarize computes the overall cell (cell == the full observation
// sequence, all observations match).
func Summarize(cell []Observation, blockLen, reps int, seed uint64) Summary {
	return SummarizeMatch(cell, cell, func(Observation) bool { return true }, blockLen, reps, seed)
}

// SummarizeMatch computes a conditioned cell: `cell` holds the observations
// belonging to the cell (e.g. one regime) for descriptive stats, while the
// bootstrap resamples the FULL chronological sequence `full` and counts
// only observations where match() holds — preserving real time adjacency.
// For the overall cell pass cell == full and an always-true match.
func SummarizeMatch(cell, full []Observation, match func(Observation) bool, blockLen, reps int, seed uint64) Summary {
	s := Summary{CIStatus: StatusInsufficient, BlockLength: blockLen}
	s.BaselineCount = len(cell)
	var ev, ctl []float64
	for _, o := range cell {
		if o.Event {
			ev = append(ev, o.Ret)
		} else {
			ctl = append(ctl, o.Ret)
		}
	}
	s.EventCount, s.ControlCount = len(ev), len(ctl)
	if len(cell) > 0 {
		m, md := meanMedF(collect(cell))
		s.BaselineMean, s.BaselineMedian = &m, &md
		pp := pctPosF(collect(cell))
		s.BaselinePosPct = &pp
	}
	if len(ctl) > 0 {
		m, md := meanMedF(ctl)
		s.ControlMean, s.ControlMedian = &m, &md
	}
	if len(ev) > 0 {
		m, md := meanMedF(ev)
		s.EventMean, s.EventMedian = &m, &md
		pp := pctPosF(ev)
		s.EventPositivePct = &pp
		w := minF(ev)
		s.WorstEventReturn = &w
		if s.BaselineMean != nil {
			d := m - *s.BaselineMean
			s.DiffMeanBaseline = &d
			dm := md - *s.BaselineMedian
			s.DiffMedianBaseline = &dm
		}
		if s.ControlMean != nil {
			d := m - *s.ControlMean
			s.DiffMeanControl = &d
			dm := md - *s.ControlMedian
			s.DiffMedianControl = &dm
		}
	}
	if len(ev) < MinEvents || len(ctl) < MinControls {
		return s
	}
	low, high, valid, ok := blockBootstrap(full, match, blockLen, reps, seed)
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

// blockBootstrap resamples obs in complete blocks: each block's start is
// uniform over the n-blockLen+1 valid positions; blocks are appended until
// the resampled length reaches n; only the final block is truncated, so
// every replicate has exactly n observations. Within a replicate, only
// observations where match() holds count as event/control. Prefix sums
// over match keep each block contribution O(1).
//
// Returns the 2.5%/97.5% percentiles of mean(event) − mean(control) across
// valid replicates; ok=false when too few valid replicates remain.
func blockBootstrap(obs []Observation, match func(Observation) bool, blockLen, reps int, seed uint64) (float64, float64, int, bool) {
	n := len(obs)
	if blockLen < 1 {
		blockLen = 1
	}
	if blockLen > n {
		blockLen = n
	}
	// Prefix sums restricted to match(): evN/ctlN counts, evS/ctlS sums.
	evN := make([]float64, n+1)
	ctlN := make([]float64, n+1)
	evS := make([]float64, n+1)
	ctlS := make([]float64, n+1)
	for i, o := range obs {
		evN[i+1], ctlN[i+1] = evN[i], ctlN[i]
		evS[i+1], ctlS[i+1] = evS[i], ctlS[i]
		if !match(o) {
			continue
		}
		if o.Event {
			evN[i+1]++
			evS[i+1] += o.Ret
		} else {
			ctlN[i+1]++
			ctlS[i+1] += o.Ret
		}
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	starts := n - blockLen + 1 // valid complete-block start positions
	diffs := make([]float64, 0, reps)
	for r := 0; r < reps; r++ {
		var en, cn, es, cs, total float64
		for total < float64(n) {
			s := rng.IntN(starts)
			e := s + blockLen
			if rem := n - int(total); e-s > rem {
				e = s + rem // truncate only the final block
			}
			en += evN[e] - evN[s]
			cn += ctlN[e] - ctlN[s]
			es += evS[e] - evS[s]
			cs += ctlS[e] - ctlS[s]
			total += float64(e - s)
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

func collect(obs []Observation) []float64 {
	vs := make([]float64, len(obs))
	for i, o := range obs {
		vs[i] = o.Ret
	}
	return vs
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
