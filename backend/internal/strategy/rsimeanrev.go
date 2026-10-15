package strategy

import (
	"context"
	"fmt"
	"math"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy/indicators"
)

// RSIMeanReversion is the RSI threshold-cross strategy (rsi-mean-reversion
// v1.0.0). Pure: no database, no HTTP, deterministic.
//
// Long-only mean-reversion:
//
//	BUY  — RSI crosses UP through the oversold threshold
//	       (prev ≤ oversold AND current > oversold)
//	SELL — RSI crosses DOWN through the overbought threshold
//	       (prev ≥ overbought AND current < overbought) — closes a long,
//	       never opens a short (the engine is long-only anyway)
//	NEUTRAL otherwise — a cross is an event: staying beyond the threshold
//	on consecutive bars does NOT repeat the signal.
//
// These rules are a research hypothesis, not a claim of trading edge.
type RSIMeanReversion struct {
	def        Definition
	period     int
	oversold   float64
	overbought float64
}

// NewRSIMeanReversion builds the strategy; defaults period=14, 30/70.
func NewRSIMeanReversion() *RSIMeanReversion {
	return &RSIMeanReversion{
		period: 14, oversold: 30, overbought: 70,
		def: Definition{
			ID:              "rsi-mean-reversion",
			Name:            "RSI Mean Reversion",
			Version:         "1.0.0",
			Description:     "Long-only mean reversion: buys when Wilder RSI crosses up through the oversold threshold, exits when it crosses down through overbought.",
			SupportedAssets: []string{"xauusd"},
			Timeframe:       market.Timeframe1d,
			SupportedTimeframes: []market.Timeframe{
				market.Timeframe15m, market.Timeframe1h,
				market.Timeframe4h, market.Timeframe1d,
			},
			Status: "active",
			Parameters: []ParameterSpec{
				{Name: "rsi_period", Label: "RSI Period", Type: "number", Default: 14.0, Min: f64(2), Max: f64(100), Step: f64(1)},
				{Name: "oversold", Label: "Oversold", Type: "number", Default: 30.0, Min: f64(1), Max: f64(49), Step: f64(1)},
				{Name: "overbought", Label: "Overbought", Type: "number", Default: 70.0, Min: f64(51), Max: f64(99), Step: f64(1)},
			},
		},
	}
}

func (r *RSIMeanReversion) ID() string                  { return r.def.ID }
func (r *RSIMeanReversion) Name() string                { return r.def.Name }
func (r *RSIMeanReversion) Version() string             { return r.def.Version }
func (r *RSIMeanReversion) SupportedAssets() []string   { return r.def.SupportedAssets }
func (r *RSIMeanReversion) Timeframe() market.Timeframe { return r.def.Timeframe }
func (r *RSIMeanReversion) Parameters() []ParameterSpec { return r.def.Parameters }
func (r *RSIMeanReversion) Describe() Definition        { return r.def }

// Configure applies validated parameter overrides: integer period in
// [2,100], thresholds in [1,49]/[51,99], oversold < overbought.
func (r *RSIMeanReversion) Configure(params Parameters) (Strategy, error) {
	c := *r
	var err error
	if c.period, err = rsiIntParam(params, "rsi_period", r.period, 2, 100); err != nil {
		return nil, err
	}
	if c.oversold, err = rsiNumParam(params, "oversold", r.oversold, 1, 49); err != nil {
		return nil, err
	}
	if c.overbought, err = rsiNumParam(params, "overbought", r.overbought, 51, 99); err != nil {
		return nil, err
	}
	if c.oversold >= c.overbought {
		return nil, fmt.Errorf("rsi-mean-reversion: oversold (%v) must be < overbought (%v)", c.oversold, c.overbought)
	}
	c.def.Parameters = []ParameterSpec{
		{Name: "rsi_period", Label: "RSI Period", Type: "number", Default: float64(c.period), Min: f64(2), Max: f64(100), Step: f64(1)},
		{Name: "oversold", Label: "Oversold", Type: "number", Default: c.oversold, Min: f64(1), Max: f64(49), Step: f64(1)},
		{Name: "overbought", Label: "Overbought", Type: "number", Default: c.overbought, Min: f64(51), Max: f64(99), Step: f64(1)},
	}
	return &c, nil
}

// WarmupBars returns 3× period: RSI is defined after `period` changes and
// Wilder smoothing needs history to converge — three periods is the
// documented convergence allowance, matching batch Evaluate semantics.
func (r *RSIMeanReversion) WarmupBars() int { return r.period*3 + 2 }

// Evaluate inspects the last two defined RSI values for a threshold cross.
func (r *RSIMeanReversion) Evaluate(_ context.Context, candles []market.Candle) (Signal, error) {
	if len(candles) < r.period+2 {
		return Signal{}, fmt.Errorf("rsi-mean-reversion: need at least %d candles, got %d", r.period+2, len(candles))
	}
	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}
	rsi := indicators.RSI(closes, r.period)
	i := len(candles) - 1
	if math.IsNaN(rsi[i-1]) || math.IsNaN(rsi[i]) {
		return Signal{}, fmt.Errorf("rsi-mean-reversion: RSI undefined at last bars (need period+%d warmup)", r.period)
	}
	return r.crossSignal(rsi[i-1], rsi[i], candles[i]), nil
}

func (r *RSIMeanReversion) crossSignal(prevRSI, curRSI float64, bar market.Candle) Signal {
	base := Signal{
		StrategyID: r.def.ID,
		Price:      bar.Close,
		Timestamp:  bar.Time,
		Metadata: map[string]any{
			"rsi": curRSI, "prev_rsi": prevRSI,
			"rsi_period": r.period, "oversold": r.oversold, "overbought": r.overbought,
		},
	}
	switch {
	case prevRSI <= r.oversold && curRSI > r.oversold:
		base.Action = ActionBuy
		base.Reason = fmt.Sprintf("RSI%d crossed above oversold %.0f", r.period, r.oversold)
	case prevRSI >= r.overbought && curRSI < r.overbought:
		base.Action = ActionSell
		base.Reason = fmt.Sprintf("RSI%d crossed below overbought %.0f", r.period, r.overbought)
	default:
		base.Action = ActionNeutral
		base.Reason = "no threshold cross"
	}
	base.Score = crossMoveScore(prevRSI, curRSI, base.Action)
	return base
}

// crossMoveScore is the SIZE OF THE CROSSING MOVE in RSI points, normalized
// to [0,1] over a 10-point reference move — identical semantics for BUY and
// SELL. NEUTRAL = 0 (no event). It is signal strength only, NOT a
// probability of profit.
func crossMoveScore(prevRSI, curRSI float64, action Action) float64 {
	if action != ActionBuy && action != ActionSell {
		return 0
	}
	s := math.Abs(curRSI-prevRSI) / 10
	if s > 1 {
		return 1
	}
	return s
}

// Streamer support: O(1) per-bar evaluation for the backtest engine.

// NewStream returns a stateful evaluator; feed bars in chronological order.
func (r *RSIMeanReversion) NewStream() Stream {
	return &rsiStream{s: r, rsi: indicators.NewRSIState(r.period)}
}

type rsiStream struct {
	s       *RSIMeanReversion
	rsi     *indicators.RSIState
	prevRSI float64
	bars    int
}

// Next consumes one candle and returns the signal for that bar. Bars before
// the RSI is defined (first `period` changes) are warm-up → NEUTRAL.
func (st *rsiStream) Next(c market.Candle) Signal {
	v, defined := st.rsi.Next(c.Close)
	st.bars++
	if !defined {
		return Signal{StrategyID: st.s.def.ID, Action: ActionNeutral, Price: c.Close, Timestamp: c.Time, Reason: "warm-up"}
	}
	if st.bars <= st.s.period+1 { // need prev+cur pair → first defined bar is warm-up too
		st.prevRSI = v
		return Signal{StrategyID: st.s.def.ID, Action: ActionNeutral, Price: c.Close, Timestamp: c.Time, Reason: "warm-up"}
	}
	sig := st.s.crossSignal(st.prevRSI, v, c)
	st.prevRSI = v
	return sig
}

func rsiIntParam(m map[string]any, key string, def, min, max int) (int, error) {
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
		return 0, fmt.Errorf("rsi-mean-reversion: %s must be a number, got %T", key, v)
	}
	i := int(f)
	if f != float64(i) {
		return 0, fmt.Errorf("rsi-mean-reversion: %s must be an integer, got %v", key, f)
	}
	if i < min || i > max {
		return 0, fmt.Errorf("rsi-mean-reversion: %s must be in [%d, %d], got %d", key, min, max, i)
	}
	return i, nil
}

func rsiNumParam(m map[string]any, key string, def, min, max float64) (float64, error) {
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
		return 0, fmt.Errorf("rsi-mean-reversion: %s must be a number, got %T", key, v)
	}
	if f < min || f > max {
		return 0, fmt.Errorf("rsi-mean-reversion: %s must be in [%v, %v], got %v", key, min, max, f)
	}
	return f, nil
}
