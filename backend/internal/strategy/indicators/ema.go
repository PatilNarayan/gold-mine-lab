// Package indicators holds pure, deterministic technical-indicator math.
// No I/O, no time, no global state.
package indicators

import "fmt"

// EMA returns the exponential moving average of values, aligned to the input
// slice (result[i] is the EMA after observing values[i]).
//
// Convention: seeded at values[0], then k = 2/(period+1) applied forward —
// the same convention the chart overlays use, so strategy and visuals agree.
// Entries before index period-1 carry less history; callers decide how much
// warm-up to demand.
func EMA(values []float64, period int) []float64 {
	if period <= 0 {
		panic(fmt.Sprintf("indicators.EMA: period %d must be positive", period))
	}
	out := make([]float64, len(values))
	if len(values) == 0 {
		return out
	}
	k := 2.0 / (float64(period) + 1)
	prev := values[0]
	out[0] = prev
	for i := 1; i < len(values); i++ {
		prev = values[i]*k + prev*(1-k)
		out[i] = prev
	}
	return out
}

// EMAState is a running EMA for O(1) per-bar updates (streaming evaluation).
type EMAState struct {
	k       float64
	value   float64
	started bool
}

// NewEMAState starts a streaming EMA seeded by the first observed value.
func NewEMAState(period int) *EMAState {
	if period <= 0 {
		panic(fmt.Sprintf("indicators.NewEMAState: period %d must be positive", period))
	}
	return &EMAState{k: 2.0 / (float64(period) + 1)}
}

// Next feeds one value and returns the updated EMA.
func (e *EMAState) Next(v float64) float64 {
	if !e.started {
		e.value = v
		e.started = true
		return e.value
	}
	e.value = v*e.k + e.value*(1-e.k)
	return e.value
}
