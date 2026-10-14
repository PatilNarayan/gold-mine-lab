package strategy

import (
	"context"
	"fmt"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy/indicators"
)

// EMACross is the EMA fast/slow crossover strategy (ema-cross v1.0.0).
// Pure: no database, no HTTP, deterministic.
//
// Rules (evaluated at candle close on the last bar of the input):
//
//	BUY  — previous fast EMA <= previous slow EMA AND current fast > current slow
//	SELL — previous fast EMA >= previous slow EMA AND current fast < current slow
//	NEUTRAL otherwise. A cross must actually occur; merely being above/below
//	the slow EMA is not a signal.
//
// Score: |fast - slow| / price * 20 clamped to [0,1] — a rough measure of how
// decisively the EMAs separated at the cross.
type EMACross struct {
	def  Definition
	fast int
	slow int
}

// NewEMACross builds the strategy; defaults are fast=20, slow=50.
func NewEMACross() *EMACross {
	return &EMACross{
		fast: 20,
		slow: 50,
		def: Definition{
			ID:              "ema-cross",
			Name:            "EMA Crossover",
			Version:         "1.0.0",
			Description:     "Trend-following: enters long when the fast EMA crosses above the slow EMA, exits on the downward cross.",
			SupportedAssets: []string{"xauusd"},
			Timeframe:       market.Timeframe1h,
			Status:          "active",
			Parameters: []ParameterSpec{
				{Name: "fast_period", Label: "Fast EMA", Type: "number", Default: 20.0, Min: f64(2), Max: f64(100), Step: f64(1)},
				{Name: "slow_period", Label: "Slow EMA", Type: "number", Default: 50.0, Min: f64(10), Max: f64(300), Step: f64(1)},
			},
		},
	}
}

func (e *EMACross) ID() string                  { return e.def.ID }
func (e *EMACross) Name() string                { return e.def.Name }
func (e *EMACross) Version() string             { return e.def.Version }
func (e *EMACross) SupportedAssets() []string   { return e.def.SupportedAssets }
func (e *EMACross) Timeframe() market.Timeframe { return e.def.Timeframe }
func (e *EMACross) Parameters() []ParameterSpec { return e.def.Parameters }

// Describe returns the serializable descriptor served by the API.
func (e *EMACross) Describe() Definition { return e.def }

// Configurable is implemented by strategies that accept runtime parameter
// overrides (validated) and return a configured copy.
type Configurable interface {
	Configure(params Parameters) (Strategy, error)
}

// Configure returns a copy of the strategy with parameter overrides applied.
// fast_period must be < slow_period, both positive.
func (e *EMACross) Configure(params Parameters) (Strategy, error) {
	c := *e
	c.fast = intParam(params, "fast_period", e.fast)
	c.slow = intParam(params, "slow_period", e.slow)
	if c.fast <= 0 || c.slow <= 0 {
		return nil, fmt.Errorf("ema-cross: periods must be positive (fast=%d slow=%d)", c.fast, c.slow)
	}
	if c.fast >= c.slow {
		return nil, fmt.Errorf("ema-cross: fast_period (%d) must be < slow_period (%d)", c.fast, c.slow)
	}
	c.def.Parameters = []ParameterSpec{
		{Name: "fast_period", Label: "Fast EMA", Type: "number", Default: float64(c.fast), Min: f64(2), Max: f64(100), Step: f64(1)},
		{Name: "slow_period", Label: "Slow EMA", Type: "number", Default: float64(c.slow), Min: f64(10), Max: f64(300), Step: f64(1)},
	}
	return &c, nil
}

// Evaluate inspects the last two bars of candles for a fast/slow cross.
// It never looks beyond the last input bar — caller controls the window.
func (e *EMACross) Evaluate(_ context.Context, candles []market.Candle) (Signal, error) {
	if len(candles) < e.slow+2 {
		return Signal{}, fmt.Errorf("ema-cross: need at least %d candles, got %d", e.slow+2, len(candles))
	}
	i := len(candles) - 1
	closes := make([]float64, len(candles))
	for j, c := range candles {
		closes[j] = c.Close
	}
	fast := indicators.EMA(closes, e.fast)
	slow := indicators.EMA(closes, e.slow)
	return e.crossSignal(fast[i-1], slow[i-1], fast[i], slow[i], candles[i]), nil
}

func (e *EMACross) crossSignal(prevF, prevS, curF, curS float64, bar market.Candle) Signal {
	base := Signal{
		StrategyID: e.def.ID,
		Price:      bar.Close,
		Timestamp:  bar.Time,
		Metadata: map[string]any{
			"fast_ema": curF, "slow_ema": curS,
			"fast_period": e.fast, "slow_period": e.slow,
		},
	}
	switch {
	case prevF <= prevS && curF > curS:
		base.Action = ActionBuy
		base.Reason = fmt.Sprintf("EMA%d crossed above EMA%d", e.fast, e.slow)
	case prevF >= prevS && curF < curS:
		base.Action = ActionSell
		base.Reason = fmt.Sprintf("EMA%d crossed below EMA%d", e.fast, e.slow)
	default:
		base.Action = ActionNeutral
		base.Reason = "no crossover"
	}
	base.Score = crossScore(curF, curS, bar.Close)
	return base
}

func crossScore(fast, slow, price float64) float64 {
	if price <= 0 {
		return 0
	}
	s := (fast - slow) / price * 20
	if s < 0 {
		s = -s
	}
	if s > 1 {
		return 1
	}
	return s
}

// Streamer support: O(1) per-bar evaluation for the backtest engine.

// NewStream returns a stateful evaluator; feed bars in chronological order.
func (e *EMACross) NewStream() Stream {
	return &emaStream{
		s:    e,
		fast: indicators.NewEMAState(e.fast),
		slow: indicators.NewEMAState(e.slow),
	}
}

type emaStream struct {
	s            *EMACross
	fast, slow   *indicators.EMAState
	prevF, prevS float64
	bars         int
}

// Next consumes one candle and returns the signal for that bar.
func (st *emaStream) Next(c market.Candle) Signal {
	f := st.fast.Next(c.Close)
	s := st.slow.Next(c.Close)
	st.bars++
	// Require slowPeriod+1 bars before allowing crosses — EMA seeds at the
	// first value, so early "crosses" are warm-up artifacts.
	if st.bars <= st.s.slow {
		st.prevF, st.prevS = f, s
		return Signal{StrategyID: st.s.def.ID, Action: ActionNeutral, Price: c.Close, Timestamp: c.Time, Reason: "warm-up"}
	}
	sig := st.s.crossSignal(st.prevF, st.prevS, f, s, c)
	st.prevF, st.prevS = f, s
	return sig
}

// Stream is a stateful, causal per-bar evaluator used by the backtest engine
// for O(n) passes. Strategies that don't provide one fall back to prefix
// Evaluate calls.
type Stream interface {
	Next(c market.Candle) Signal
}

// Streamer is implemented by strategies supporting incremental evaluation.
type Streamer interface {
	NewStream() Stream
}

func intParam(m map[string]any, key string, def int) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}
