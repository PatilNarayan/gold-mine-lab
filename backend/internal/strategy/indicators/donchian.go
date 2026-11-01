package indicators

import "math"

// DonchianChannels returns per-bar upper/lower channel values aligned to the
// input slices. The channel for bar i spans candles [i-lookback, i-1] — the
// bar itself never enters its own breakout reference (no lookahead). This is
// the identical semantic the donchian-breakout strategy applies; entries at
// index < lookback are NaN (undefined).
func DonchianChannels(highs, lows []float64, lookback int) (upper, lower []float64) {
	if len(highs) != len(lows) {
		panic("indicators.DonchianChannels: highs and lows must have equal length")
	}
	if lookback <= 0 {
		panic("indicators.DonchianChannels: lookback must be positive")
	}
	n := len(highs)
	upper = make([]float64, n)
	lower = make([]float64, n)
	for i := range upper {
		upper[i], lower[i] = math.NaN(), math.NaN()
	}
	for i := lookback; i < n; i++ {
		u, l := -math.MaxFloat64, math.MaxFloat64
		for j := i - lookback; j < i; j++ {
			if highs[j] > u {
				u = highs[j]
			}
			if lows[j] < l {
				l = lows[j]
			}
		}
		upper[i], lower[i] = u, l
	}
	return upper, lower
}
