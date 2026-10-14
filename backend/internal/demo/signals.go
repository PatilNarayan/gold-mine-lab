package demo

import (
	"sort"
	"strconv"
	"time"

	"github.com/gold-mine-lab/backend/internal/strategy"
)

var neutralReasons = []string{
	"No cross within threshold band",
	"Signal strength below cutoff",
	"Conflicting timeframe momentum",
	"Volatility filter suppressed entry",
}

// signalsForStrategy derives deterministic signals from the EMA overlays of
// the strategy's own candle series — buy on bullish cross, sell on bearish,
// occasional neutral readings.
func signalsForStrategy(strategyID string) []strategy.Signal {
	s, ok := strategyByID(strategyID)
	if !ok {
		return nil
	}
	r := newRng("signals|" + strategyID)
	var out []strategy.Signal

	for _, assetID := range s.SupportedAssets {
		series, err := candleSeries(assetID, s.Timeframe)
		if err != nil {
			continue
		}
		m := meta(assetID)
		e20 := make(map[string]float64, len(series.Overlays.EMA20))
		for _, p := range series.Overlays.EMA20 {
			e20[p.Time] = p.Value
		}
		e50 := make(map[string]float64, len(series.Overlays.EMA50))
		for _, p := range series.Overlays.EMA50 {
			e50[p.Time] = p.Value
		}

		prevDiff := 0.0
		n := 0
		for _, c := range series.Candles {
			key := c.Time.UTC().Format(time.RFC3339)
			a, okA := e20[key]
			b, okB := e50[key]
			if !okA || !okB {
				continue
			}
			diff := a - b
			var action strategy.Action
			var reason string
			switch {
			case prevDiff <= 0 && diff > 0:
				action, reason = strategy.ActionBuy, "EMA20 crossed above EMA50"
			case prevDiff >= 0 && diff < 0:
				action, reason = strategy.ActionSell, "EMA20 crossed below EMA50"
			case r.next() < 0.025:
				action, reason = strategy.ActionNeutral, r.pick(neutralReasons)
			}
			if action != "" {
				out = append(out, strategy.Signal{
					ID:         signalID(strategyID, assetID, n),
					StrategyID: strategyID,
					AssetID:    assetID,
					Action:     action,
					Score:      round(r.between(0.3, 0.95), 2),
					Price:      c.Close,
					Timestamp:  c.Time,
					Reason:     reason,
					Metadata: map[string]any{
						"ema20": round(a, m.digits),
						"ema50": round(b, m.digits),
					},
				})
				n++
			}
			prevDiff = diff
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}

func signalID(strategyID, assetID string, n int) string {
	return "sig-" + strategyID + "-" + assetID + "-" + strconv.Itoa(n)
}

func allSignals() []strategy.Signal {
	var out []strategy.Signal
	for _, s := range strategies {
		out = append(out, signalsForStrategy(s.ID)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	return out
}

func latestSignalFor(strategyID string) *strategy.Signal {
	sigs := signalsForStrategy(strategyID)
	if len(sigs) == 0 {
		return nil
	}
	last := sigs[len(sigs)-1]
	return &last
}
