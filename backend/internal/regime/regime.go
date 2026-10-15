// Package regime provides deterministic market-regime classification for
// Signal Research (Phase 10). Two independent dimensions:
//
//	Trend      — close vs EMA50/EMA200: uptrend / downtrend / transition / unknown
//	Volatility — Wilder ATR-14 as % of close vs an EMA-100 baseline of PRIOR
//	             ATR% observations: low / normal / high / unknown
//
// Every label is computed only from candles at or before the current close —
// no future data may ever influence a label, and a label never changes once
// emitted. The volatility baseline excludes the current observation.
//
// Volatility labels are timeframe-relative: a 100-bar baseline spans a
// different elapsed period on 15m than on 1d. The 0.8/1.2 multipliers are
// initial research conventions, not optimized values.
package regime

import (
	"math"

	"github.com/gold-mine-lab/backend/internal/market"
)

// DefinitionVersion versions the exact label definitions stored with each
// analysis so saved research stays reproducible.
const DefinitionVersion = "regime-v1"

type Trend string

const (
	TrendUp         Trend = "uptrend"
	TrendDown       Trend = "downtrend"
	TrendTransition Trend = "transition"
	TrendUnknown    Trend = "unknown"
)

var AllTrends = []Trend{TrendUp, TrendDown, TrendTransition, TrendUnknown}

type Volatility string

const (
	VolLow     Volatility = "low"
	VolNormal  Volatility = "normal"
	VolHigh    Volatility = "high"
	VolUnknown Volatility = "unknown"
)

var AllVolatilities = []Volatility{VolLow, VolNormal, VolHigh, VolUnknown}

const (
	trendFastPeriod = 50
	trendSlowPeriod = 200
	atrPeriod       = 14
	baselinePeriod  = 100
	volLowMult      = 0.8
	volHighMult     = 1.2
)

// WarmupBars is the minimum history before trend labels become available
// (volatility becomes available earlier). Strategy warm-up and this value
// share the same candle prefix.
const WarmupBars = trendSlowPeriod

// ema is a streaming exponential moving average seeded on the first value;
// it reports availability only after `period` observations.
type ema struct {
	k    float64
	need int
	v    float64
	n    int
}

func newEMA(period int) *ema { return &ema{k: 2.0 / float64(period+1), need: period} }

func (e *ema) next(x float64) {
	e.n++
	if e.n == 1 {
		e.v = x
		return
	}
	e.v += e.k * (x - e.v)
}

func (e *ema) get() (float64, bool) { return e.v, e.n >= e.need }

// Classifier is fed every candle once, in chronological order, and yields
// the (trend, volatility) regime known at that candle's close.
type Classifier struct {
	emaFast  *ema
	emaSlow  *ema
	prev     float64
	havePrev bool
	trSum    float64
	trN      int
	atr      float64
	haveATR  bool
	base     *ema
	seen     int // candles fed so far
}

func New() *Classifier {
	return &Classifier{
		emaFast: newEMA(trendFastPeriod),
		emaSlow: newEMA(trendSlowPeriod),
		base:    newEMA(baselinePeriod),
	}
}

// Next consumes one candle and returns the regime at its close.
func (c *Classifier) Next(bar market.Candle) (Trend, Volatility) {
	c.seen++
	c.emaFast.next(bar.Close)
	c.emaSlow.next(bar.Close)
	trend := c.trendLabel(bar)
	vol := c.volLabel(bar)
	return trend, vol
}

func (c *Classifier) trendLabel(bar market.Candle) Trend {
	slow, ok := c.emaSlow.get()
	if !ok || math.IsNaN(slow) || math.IsNaN(bar.Close) {
		return TrendUnknown
	}
	fast, _ := c.emaFast.get() // slow implies fast history
	switch {
	case bar.Close > slow && fast > slow:
		return TrendUp
	case bar.Close < slow && fast < slow:
		return TrendDown
	default:
		return TrendTransition
	}
}

// volLabel computes ATR% for this candle, compares it to the baseline built
// exclusively from PRIOR ATR% observations, then folds this candle's ATR%
// into the baseline for subsequent candles.
func (c *Classifier) volLabel(bar market.Candle) Volatility {
	if !c.havePrev {
		c.prev = bar.Close
		c.havePrev = true
		return VolUnknown
	}
	tr := math.Max(bar.High-bar.Low,
		math.Max(math.Abs(bar.High-c.prev), math.Abs(bar.Low-c.prev)))
	c.prev = bar.Close
	if math.IsNaN(tr) || tr < 0 {
		return VolUnknown
	}
	c.trN++
	if !c.haveATR {
		c.trSum += tr
		if c.trN == atrPeriod {
			c.atr = c.trSum / atrPeriod
			c.haveATR = true
		} else {
			return VolUnknown
		}
	} else {
		c.atr = (c.atr*(atrPeriod-1) + tr) / atrPeriod
	}
	if bar.Close <= 0 || math.IsNaN(bar.Close) {
		return VolUnknown
	}
	atrp := c.atr / bar.Close * 100
	if math.IsNaN(atrp) || math.IsInf(atrp, 0) {
		return VolUnknown
	}
	baseline, ok := c.base.get()
	c.base.next(atrp) // current observation joins the baseline only afterwards
	if !ok || baseline <= 0 || math.IsNaN(baseline) {
		return VolUnknown
	}
	switch {
	case atrp < volLowMult*baseline:
		return VolLow
	case atrp > volHighMult*baseline:
		return VolHigh
	default:
		return VolNormal
	}
}
