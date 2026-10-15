// Wilder's Relative Strength Index.
//
// Convention (documented — batch and streaming agree exactly):
//
//	RSI = 100 − 100 / (1 + RS),   RS = avgGain / avgLoss
//
// avgGain/avgLoss are seeded by a SIMPLE average of the first `period`
// price changes, then smoothed by Wilder's recursion:
//
//	avgGain = (prevAvgGain·(period−1) + gain) / period
//
// Defined output begins at index `period` — entries before that are NaN.
// Edge cases: avgLoss == 0 → RSI 100 when avgGain > 0 (all-up), and 50 when
// both averages are 0 (constant prices carry no direction). NaN/±Inf inputs
// propagate as NaN — the caller validates data integrity upstream.
package indicators

import (
	"fmt"
	"math"
)

// RSI returns the Wilder RSI series aligned to the input slice.
// Result entries at index < period are NaN (undefined).
func RSI(values []float64, period int) []float64 {
	if period <= 0 {
		panic(fmt.Sprintf("indicators.RSI: period %d must be positive", period))
	}
	out := make([]float64, len(values))
	for i := range out {
		out[i] = math.NaN()
	}
	st := NewRSIState(period)
	for i, v := range values {
		if r, ok := st.Next(v); ok {
			out[i] = r
		}
	}
	return out
}

// RSIState is a running RSI for O(1) per-bar updates. Next returns
// (value, defined) — defined is false until `period` changes were consumed.
type RSIState struct {
	period  int
	avgGain float64
	avgLoss float64
	prev    float64
	changes int // price changes seen
	seeded  bool
}

// NewRSIState starts a streaming RSI.
func NewRSIState(period int) *RSIState {
	if period <= 0 {
		panic(fmt.Sprintf("indicators.NewRSIState: period %d must be positive", period))
	}
	return &RSIState{period: period}
}

// Next feeds one close and returns the RSI once defined (after `period`
// changes), plus ok=true. Before that it returns (NaN, false).
func (s *RSIState) Next(v float64) (float64, bool) {
	if !s.seeded { // first value establishes the baseline, it is not a change
		s.prev = v
		s.seeded = true
		return math.NaN(), false
	}
	ch := v - s.prev
	s.prev = v
	gain, loss := math.Max(ch, 0), math.Max(-ch, 0)
	s.changes++
	if s.changes < s.period { // inside the seeding window
		s.avgGain += gain
		s.avgLoss += loss
		return math.NaN(), false
	}
	if s.changes == s.period { // seed = simple average of the first `period` changes
		s.avgGain = (s.avgGain + gain) / float64(s.period)
		s.avgLoss = (s.avgLoss + loss) / float64(s.period)
	} else { // Wilder smoothing
		s.avgGain = (s.avgGain*float64(s.period-1) + gain) / float64(s.period)
		s.avgLoss = (s.avgLoss*float64(s.period-1) + loss) / float64(s.period)
	}
	return rsiOf(s.avgGain, s.avgLoss), true
}

func rsiOf(avgGain, avgLoss float64) float64 {
	switch {
	case math.IsNaN(avgGain) || math.IsNaN(avgLoss):
		return math.NaN()
	case avgLoss == 0:
		if avgGain == 0 {
			return 50 // constant prices — no direction, midpoint convention
		}
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs)
}
