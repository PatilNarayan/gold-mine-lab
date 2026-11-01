// Package tradeplan converts a strategy BUY signal into a forward-looking
// trade plan: an entry reference, a stop loss, and risk-multiple targets
// T1/T2/T3. It is a pure calculation layer — it consumes only information
// available at signal time (the signal plus already-closed candles) and
// performs no prediction, I/O, or execution.
//
// Levels produced here are risk/reward reference levels, not forecasts.
// Existing SELL signals are long-exit markers, never short entries.
package tradeplan

import (
	"fmt"
	"math"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// RejectReason classifies why a signal could not produce a trade plan.
type RejectReason string

const (
	RejectNotActionable       RejectReason = "not_actionable"       // signal is not an actionable BUY
	RejectUnsupportedStrategy RejectReason = "unsupported_strategy" // no stop policy registered for the strategy ID
	RejectInvalidPrice        RejectReason = "invalid_price"        // non-finite or non-positive price input
	RejectInsufficientHistory RejectReason = "insufficient_history" // not enough closed candles for ATR/swing confirmation
	RejectNoStructure         RejectReason = "no_structure"         // no confirmed structural anchor found
	RejectMissingMetadata     RejectReason = "missing_metadata"     // required signal metadata absent or malformed
	RejectStopMisplaced       RejectReason = "stop_misplaced"       // resolved stop is not below entry (long setups)
	RejectRiskOutOfLimits     RejectReason = "risk_out_of_limits"   // risk per unit outside configured bounds
	RejectInvalidConfig       RejectReason = "invalid_config"       // calculator configuration is unusable
)

// Rejection is the typed failure result of Calculate. It is returned as a
// value (not an error) because an unplannable signal is an expected outcome,
// not an exceptional one.
type Rejection struct {
	Reason RejectReason
	Detail string
}

func (r *Rejection) Error() string { return fmt.Sprintf("tradeplan: %s: %s", r.Reason, r.Detail) }

// Config tunes the calculator. Stop policies read shared knobs plus their own
// method, so strategies stay independently configurable.
type Config struct {
	ATRPeriod       int     // Wilder ATR lookback (default 14)
	SwingStrength   int     // bars required on each side to confirm a swing (default 3)
	ATRBufferMult   float64 // stop = anchor - ATR*mult (default 0.5)
	MinRiskPerUnit  float64 // absolute floor on Entry-StopLoss; 0 disables
	MaxRiskFraction float64 // cap on risk as a fraction of entry, e.g. 0.10; 0 disables
	T1Mult          float64 // reward multiple for T1 (default 1)
	T2Mult          float64 // reward multiple for T2 (default 2)
	T3Mult          float64 // reward multiple for T3 (default 3)
}

// DefaultConfig returns the spec defaults.
func DefaultConfig() Config {
	return Config{
		ATRPeriod:       14,
		SwingStrength:   3,
		ATRBufferMult:   0.5,
		MinRiskPerUnit:  0,
		MaxRiskFraction: 0.10,
		T1Mult:          1,
		T2Mult:          2,
		T3Mult:          3,
	}
}

// Input is everything the calculator may see: the signal itself, the chosen
// reference entry price, and chronological CLOSED candles ending no later
// than the signal. Supplying candles that postdate the signal leaks the
// future into the plan — callers must not do that.
type Input struct {
	Signal  strategy.Signal
	Entry   float64
	Candles []market.Candle
}

// Plan is a long trade plan derived from one BUY signal.
type Plan struct {
	StrategyID   string  `json:"strategyId"`
	Method       string  `json:"method"`     // which stop policy produced the levels
	Direction    string  `json:"direction"`  // always "LONG" — shorts are not modeled
	Entry        float64 `json:"entry"`      // reference entry price (caller-supplied)
	StopLoss     float64 `json:"stopLoss"`   // active from entry, not a scheduled action
	StopAnchor   float64 `json:"stopAnchor"` // structural level the stop derives from
	T1           float64 `json:"t1"`
	T2           float64 `json:"t2"`
	T3           float64 `json:"t3"`
	RiskPerUnit  float64 `json:"riskPerUnit"`  // Entry - StopLoss
	Invalidation string  `json:"invalidation"` // plain-language explanation of the stop
}

// Calculate evaluates one signal into a Plan. A nil *Rejection means success.
// Signal.Score is deliberately ignored — it is a strength heuristic, not a
// probability of success.
func Calculate(in Input, cfg Config) (*Plan, *Rejection) {
	if in.Signal.Action != strategy.ActionBuy {
		return nil, &Rejection{RejectNotActionable,
			fmt.Sprintf("action %q cannot produce a long entry plan (SELL signals are long exits, not short entries)", in.Signal.Action)}
	}
	if !finitePositive(in.Entry) {
		return nil, &Rejection{RejectInvalidPrice, fmt.Sprintf("entry %v is not a finite positive price", in.Entry)}
	}
	if !finitePositive(in.Signal.Price) {
		return nil, &Rejection{RejectInvalidPrice, fmt.Sprintf("signal price %v is not a finite positive price", in.Signal.Price)}
	}
	if cfg.ATRPeriod < 1 || cfg.SwingStrength < 1 || cfg.ATRBufferMult < 0 ||
		!(cfg.T1Mult > 0 && cfg.T1Mult < cfg.T2Mult && cfg.T2Mult < cfg.T3Mult) {
		return nil, &Rejection{RejectInvalidConfig,
			"require ATRPeriod>=1, SwingStrength>=1, ATRBufferMult>=0, and 0<T1<T2<T3 reward multiples"}
	}

	// Need ATRPeriod TRs (=> period+1 candles) AND strength bars on each side
	// of any swing candidate.
	need := cfg.ATRPeriod + 1
	if sw := 2*cfg.SwingStrength + 1; sw > need {
		need = sw
	}
	if len(in.Candles) < need {
		return nil, &Rejection{RejectInsufficientHistory,
			fmt.Sprintf("need at least %d closed candles, got %d", need, len(in.Candles))}
	}
	for i, c := range in.Candles {
		if !finitePositive(c.High) || !finitePositive(c.Low) || !finitePositive(c.Close) || c.High < c.Low {
			return nil, &Rejection{RejectInvalidPrice,
				fmt.Sprintf("candle %d has invalid OHLC (H=%v L=%v C=%v)", i, c.High, c.Low, c.Close)}
		}
	}

	atr := wilderATR(in.Candles, cfg.ATRPeriod)

	anchor, stop, method, invalidation, rej := stopFor(in.Signal.StrategyID, in, cfg, atr)
	if rej != nil {
		return nil, rej
	}
	if !(stop < in.Entry) {
		return nil, &Rejection{RejectStopMisplaced,
			fmt.Sprintf("resolved stop %.6f (anchor %.6f - %.2f ATR) is not below entry %.6f", stop, anchor, cfg.ATRBufferMult, in.Entry)}
	}

	risk := in.Entry - stop
	if cfg.MinRiskPerUnit > 0 && risk < cfg.MinRiskPerUnit {
		return nil, &Rejection{RejectRiskOutOfLimits,
			fmt.Sprintf("risk %.6f below configured minimum %.6f", risk, cfg.MinRiskPerUnit)}
	}
	if cfg.MaxRiskFraction > 0 && risk > in.Entry*cfg.MaxRiskFraction {
		return nil, &Rejection{RejectRiskOutOfLimits,
			fmt.Sprintf("risk %.6f exceeds %.0f%% of entry %.6f", risk, cfg.MaxRiskFraction*100, in.Entry)}
	}

	return &Plan{
		StrategyID:   in.Signal.StrategyID,
		Method:       method,
		Direction:    "LONG",
		Entry:        in.Entry,
		StopLoss:     stop,
		StopAnchor:   anchor,
		T1:           in.Entry + risk*cfg.T1Mult,
		T2:           in.Entry + risk*cfg.T2Mult,
		T3:           in.Entry + risk*cfg.T3Mult,
		RiskPerUnit:  risk,
		Invalidation: invalidation,
	}, nil
}

func finitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}
