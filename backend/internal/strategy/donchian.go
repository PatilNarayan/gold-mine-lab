package strategy

import (
	"context"
	"fmt"
	"math"

	"github.com/gold-mine-lab/backend/internal/market"
)

// DonchianBreakout is the Donchian Channel breakout strategy
// (donchian-breakout v1.0.0). Pure: no database, no HTTP, deterministic.
//
// Long-only channel breakout:
//
//	BUY  — close > highest high of the PRECEDING `lookback_period` candles
//	       (the current candle's own high/low never enters its channel)
//	SELL — close < lowest low of the PRECEDING `lookback_period` candles —
//	       closes a long, never opens a short (the engine is long-only)
//	NEUTRAL otherwise.
//
// A breakout is a continuing condition, not a one-time cross: while the
// close keeps making new channel extremes the signal repeats — no
// fabricated "crossing" semantics.
//
// These rules are a research hypothesis, not a claim of trading edge.
type DonchianBreakout struct {
	def      Definition
	lookback int
}

// NewDonchianBreakout builds the strategy; default lookback_period=20.
func NewDonchianBreakout() *DonchianBreakout {
	return &DonchianBreakout{
		lookback: 20,
		def: Definition{
			ID:              "donchian-breakout",
			Name:            "Donchian Breakout",
			Version:         "1.0.0",
			Description:     "Long-only breakout: buys when the close exceeds the highest high of the preceding N candles, exits when it breaks the lowest low of the preceding N.",
			SupportedAssets: []string{"xauusd"},
			Timeframe:       market.Timeframe1d,
			SupportedTimeframes: []market.Timeframe{
				market.Timeframe15m, market.Timeframe1h,
				market.Timeframe4h, market.Timeframe1d,
			},
			Status: "active",
			Parameters: []ParameterSpec{
				{Name: "lookback_period", Label: "Lookback Period", Type: "number", Default: 20.0, Min: f64(5), Max: f64(200), Step: f64(1)},
			},
		},
	}
}

func (d *DonchianBreakout) ID() string                  { return d.def.ID }
func (d *DonchianBreakout) Name() string                { return d.def.Name }
func (d *DonchianBreakout) Version() string             { return d.def.Version }
func (d *DonchianBreakout) SupportedAssets() []string   { return d.def.SupportedAssets }
func (d *DonchianBreakout) Timeframe() market.Timeframe { return d.def.Timeframe }
func (d *DonchianBreakout) Parameters() []ParameterSpec { return d.def.Parameters }
func (d *DonchianBreakout) Describe() Definition        { return d.def }

// Configure applies validated parameter overrides: integer lookback in
// [5,200].
func (d *DonchianBreakout) Configure(params Parameters) (Strategy, error) {
	c := *d
	var err error
	if c.lookback, err = donchianIntParam(params, "lookback_period", d.lookback, 5, 200); err != nil {
		return nil, err
	}
	c.def.Parameters = []ParameterSpec{
		{Name: "lookback_period", Label: "Lookback Period", Type: "number", Default: float64(c.lookback), Min: f64(5), Max: f64(200), Step: f64(1)},
	}
	return &c, nil
}

// WarmupBars returns the lookback period: a bar's channel needs N preceding
// candles, so the first evaluated bar must have N bars of history behind it.
func (d *DonchianBreakout) WarmupBars() int { return d.lookback }

// channelSignal compares the current close against the channel of the N
// preceding candles (upper/lower already computed, current bar excluded).
func (d *DonchianBreakout) channelSignal(upper, lower float64, bar market.Candle) Signal {
	width := upper - lower
	sig := Signal{
		StrategyID: d.def.ID,
		Price:      bar.Close,
		Timestamp:  bar.Time,
		Metadata: map[string]any{
			"upper_channel":   upper,
			"lower_channel":   lower,
			"channel_width":   width,
			"lookback_period": d.lookback,
			"close":           bar.Close,
		},
	}
	switch {
	case bar.Close > upper:
		dist := bar.Close - upper
		sig.Action = ActionBuy
		sig.Reason = fmt.Sprintf("close %.2f above upper channel %.2f", bar.Close, upper)
		sig.Score = donchianScore(dist, width)
	case bar.Close < lower:
		dist := lower - bar.Close
		sig.Action = ActionSell
		sig.Reason = fmt.Sprintf("close %.2f below lower channel %.2f", bar.Close, lower)
		sig.Score = donchianScore(dist, width)
	default:
		sig.Action = ActionNeutral
		sig.Reason = "no breakout"
	}
	return sig
}

// donchianScore normalizes breakout distance relative to the preceding
// channel width: dist / (width + dist) in [0,1). A heuristic measuring how
// far the close exceeded the channel — signal strength, NOT a probability
// of profit. Zero channel width with a real breakout returns 1.
func donchianScore(dist, width float64) float64 {
	if dist <= 0 || math.IsNaN(dist) || math.IsNaN(width) || math.IsInf(dist, 0) || math.IsInf(width, 0) {
		return 0
	}
	denom := width + dist
	if denom <= 0 {
		return 1 // width must be 0 (both non-negative, dist>0)
	}
	s := dist / denom
	if s > 1 {
		return 1
	}
	return s
}

// Evaluate computes the channel over the candles BEFORE the last one and
// emits the signal for the last bar — identical semantics to Next.
func (d *DonchianBreakout) Evaluate(_ context.Context, candles []market.Candle) (Signal, error) {
	if len(candles) < d.lookback+1 {
		return Signal{}, fmt.Errorf("donchian-breakout: need at least %d candles, got %d", d.lookback+1, len(candles))
	}
	last := candles[len(candles)-1]
	upper, lower := -math.MaxFloat64, math.MaxFloat64
	for _, c := range candles[len(candles)-1-d.lookback : len(candles)-1] {
		if c.High > upper {
			upper = c.High
		}
		if c.Low < lower {
			lower = c.Low
		}
	}
	return d.channelSignal(upper, lower, last), nil
}

// Streamer support: amortized O(1) per-bar evaluation via a monotonic deque.

// NewStream returns a stateful evaluator; feed bars in chronological order.
func (d *DonchianBreakout) NewStream() Stream {
	return &donchianStream{
		s: d,
		ring: &extremaRing{
			cap:   d.lookback,
			highs: make([]float64, d.lookback),
			lows:  make([]float64, d.lookback),
		},
	}
}

// extremaRing keeps the last `cap` highs/lows and answers rolling max/min
// in amortized O(1) via monotonic deques.
type extremaRing struct {
	cap    int
	highs  []float64 // ring buffer, index = barIndex mod cap
	lows   []float64
	dqMax  []int // bar indices, strictly decreasing highs
	dqMin  []int // bar indices, strictly increasing lows
	barIdx int   // next bar index to store
	count  int
}

func (r *extremaRing) push(high, low float64) {
	i := r.barIdx % r.cap
	r.highs[i] = high
	r.lows[i] = low
	// Evict elements falling outside the rolling window.
	for len(r.dqMax) > 0 && r.dqMax[0] <= r.barIdx-r.cap {
		r.dqMax = r.dqMax[1:]
	}
	for len(r.dqMin) > 0 && r.dqMin[0] <= r.barIdx-r.cap {
		r.dqMin = r.dqMin[1:]
	}
	for len(r.dqMax) > 0 && r.highs[r.dqMax[len(r.dqMax)-1]%r.cap] <= high {
		r.dqMax = r.dqMax[:len(r.dqMax)-1]
	}
	for len(r.dqMin) > 0 && r.lows[r.dqMin[len(r.dqMin)-1]%r.cap] >= low {
		r.dqMin = r.dqMin[:len(r.dqMin)-1]
	}
	r.dqMax = append(r.dqMax, r.barIdx)
	r.dqMin = append(r.dqMin, r.barIdx)
	r.barIdx++
	if r.count < r.cap {
		r.count++
	}
}

func (r *extremaRing) upper() float64 { return r.highs[r.dqMax[0]%r.cap] }
func (r *extremaRing) lower() float64 { return r.lows[r.dqMin[0]%r.cap] }

type donchianStream struct {
	s    *DonchianBreakout
	ring *extremaRing
}

// Next consumes one candle and returns the signal for that bar. The channel
// is read BEFORE the current candle's high/low are pushed, so a bar never
// enters its own channel. Bars before `lookback` predecessors exist are
// warm-up → NEUTRAL.
func (st *donchianStream) Next(c market.Candle) Signal {
	if st.ring.count < st.s.lookback {
		st.ring.push(c.High, c.Low)
		return Signal{StrategyID: st.s.def.ID, Action: ActionNeutral, Price: c.Close, Timestamp: c.Time, Reason: "warm-up"}
	}
	sig := st.s.channelSignal(st.ring.upper(), st.ring.lower(), c)
	st.ring.push(c.High, c.Low)
	return sig
}

func donchianIntParam(m map[string]any, key string, def, min, max int) (int, error) {
	v, ok := m[key]
	if !ok {
		return def, nil
	}
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	default:
		return 0, fmt.Errorf("donchian-breakout: %s must be a number, got %T", key, v)
	}
	i := int(f)
	if f != float64(i) {
		return 0, fmt.Errorf("donchian-breakout: %s must be an integer, got %v", key, f)
	}
	if i < min || i > max {
		return 0, fmt.Errorf("donchian-breakout: %s must be in [%d, %d], got %d", key, min, max, i)
	}
	return i, nil
}
