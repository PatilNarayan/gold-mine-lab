package tradeplan

import (
	"fmt"
	"math"

	"github.com/gold-mine-lab/backend/internal/market"
)

// wilderATR computes Wilder's ATR over the full candle slice: a simple
// average of the first `period` true ranges, then Wilder smoothing. The
// caller guarantees len(candles) >= period+1. Returns the ATR as of the
// last candle.
func wilderATR(candles []market.Candle, period int) float64 {
	trs := make([]float64, 0, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		trs = append(trs, trueRange(candles[i], candles[i-1].Close))
	}
	var atr float64
	for i := 0; i < period; i++ {
		atr += trs[i]
	}
	atr /= float64(period)
	for i := period; i < len(trs); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}
	return atr
}

func trueRange(c market.Candle, prevClose float64) float64 {
	return math.Max(c.High-c.Low, math.Max(math.Abs(c.High-prevClose), math.Abs(c.Low-prevClose)))
}

// latestSwingLow returns the LOW of the most recent confirmed swing low, or
// ok=false. A swing at index i is confirmed only when `strength` candles
// exist on BOTH sides inside the supplied slice — so the newest confirmable
// swing is at index len-1-strength; anything newer is unconfirmed and never
// used. No candle after i participates in judging i.
func latestSwingLow(candles []market.Candle, strength int) (float64, bool) {
	for i := len(candles) - 1 - strength; i >= strength; i-- {
		low := candles[i].Low
		isSwing := true
		for j := i - strength; j <= i+strength && isSwing; j++ {
			if j != i && candles[j].Low <= low {
				isSwing = false
			}
		}
		if isSwing {
			return low, true
		}
	}
	return 0, false
}

// stopFor dispatches to the strategy-specific stop policy.
func stopFor(strategyID string, in Input, cfg Config, atr float64) (
	anchor, stop float64, method, invalidation string, rej *Rejection,
) {
	switch strategyID {
	case "ema-cross":
		return emaCrossStop(in, cfg, atr)
	case "rsi-mean-reversion":
		return rsiMeanRevStop(in, cfg, atr)
	case "donchian-breakout":
		return donchianStop(in, cfg, atr)
	default:
		return 0, 0, "", "", &Rejection{RejectUnsupportedStrategy,
			fmt.Sprintf("no stop-loss policy registered for strategy %q", strategyID)}
	}
}

// emaCrossStop anchors on the latest confirmed swing low.
func emaCrossStop(in Input, cfg Config, atr float64) (float64, float64, string, string, *Rejection) {
	low, ok := latestSwingLow(in.Candles, cfg.SwingStrength)
	if !ok {
		return 0, 0, "", "", &Rejection{RejectNoStructure,
			"no confirmed swing low in the supplied history (needs candles on both sides)"}
	}
	stop := low - atr*cfg.ATRBufferMult
	return low, stop, "swing-low-atr",
		fmt.Sprintf("invalid if price closes below the confirmed swing low at %.6f (stop %.6f = swing - %.2f×ATR)", low, stop, cfg.ATRBufferMult), nil
}

// rsiMeanRevStop anchors on the confirmed reversal-zone swing low — the most
// recent confirmed low, which for an oversold-reversal BUY is the reversal
// base itself. Kept as a separate policy so its anchor rule can diverge
// (e.g. zone-bounded search) without touching EMA.
func rsiMeanRevStop(in Input, cfg Config, atr float64) (float64, float64, string, string, *Rejection) {
	low, ok := latestSwingLow(in.Candles, cfg.SwingStrength)
	if !ok {
		return 0, 0, "", "", &Rejection{RejectNoStructure,
			"reversal structure cannot be established — no confirmed swing low in the supplied history"}
	}
	stop := low - atr*cfg.ATRBufferMult
	return low, stop, "reversal-swing-atr",
		fmt.Sprintf("invalid if price loses the reversal base at %.6f (stop %.6f = swing - %.2f×ATR)", low, stop, cfg.ATRBufferMult), nil
}

// donchianStop anchors on the upper_channel boundary carried in the BUY
// signal's metadata — the breakout level whose loss invalidates the setup.
// Missing or malformed metadata is rejected, never invented.
func donchianStop(in Input, cfg Config, atr float64) (float64, float64, string, string, *Rejection) {
	raw, ok := in.Signal.Metadata["upper_channel"]
	if !ok {
		return 0, 0, "", "", &Rejection{RejectMissingMetadata, "signal metadata lacks \"upper_channel\""}
	}
	upper, ok := raw.(float64)
	if !ok || !finitePositive(upper) {
		return 0, 0, "", "", &Rejection{RejectMissingMetadata,
			fmt.Sprintf("\"upper_channel\" metadata is not a finite positive number: %v", raw)}
	}
	stop := upper - atr*cfg.ATRBufferMult
	return upper, stop, "channel-invalidation-atr",
		fmt.Sprintf("invalid if price falls back below the broken channel boundary %.6f (stop %.6f = boundary - %.2f×ATR)", upper, stop, cfg.ATRBufferMult), nil
}
