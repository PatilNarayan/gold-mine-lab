package demo

import (
	"math"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
)

// Fixed historical window — synthetic candles always end at a past date and
// never imply "now".
var (
	historyEnd   = time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	historyStart = time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
)

const candleCount = 240

var timeframeStep = map[market.Timeframe]time.Duration{
	market.Timeframe1m:  time.Minute,
	market.Timeframe5m:  5 * time.Minute,
	market.Timeframe15m: 15 * time.Minute,
	market.Timeframe1h:  time.Hour,
	market.Timeframe4h:  4 * time.Hour,
	market.Timeframe1d:  24 * time.Hour,
}

// ema computes an exponential moving average; positions before the warmup
// period are zero-filled and skipped when building series points.
func ema(values []float64, period int) []float64 {
	k := 2.0 / float64(period+1)
	out := make([]float64, len(values))
	var prev float64
	for i := range values {
		if i < period-1 {
			continue
		}
		if i == period-1 {
			var sum float64
			for j := i - period + 1; j <= i; j++ {
				sum += values[j]
			}
			prev = sum / float64(period)
		} else {
			prev = values[i]*k + prev*(1-k)
		}
		out[i] = prev
	}
	return out
}

// candleSeries generates a deterministic OHLCV series with EMA20/EMA50
// overlays — the same response shape the real provider will return.
func candleSeries(assetID string, tf market.Timeframe) (api.CandleSeries, error) {
	m := meta(assetID)
	step, ok := timeframeStep[tf]
	if !ok {
		return api.CandleSeries{}, &api.ValidationError{Msg: "unsupported timeframe " + string(tf)}
	}
	if _, ok := assetByID(assetID); !ok {
		return api.CandleSeries{}, api.ErrNotFound
	}

	r := newRng("candles|" + assetID + "|" + string(tf))
	drift := r.between(-0.0006, 0.0011)
	vol := m.volatility

	candles := make([]market.Candle, 0, candleCount)
	prevClose := m.basePrice * (1 + r.between(-0.03, 0.03))
	for i := 0; i < candleCount; i++ {
		t := historyEnd.Add(-time.Duration(candleCount-1-i) * step)
		shock := (r.next() - 0.5) * 2 * vol
		open := prevClose
		close := open * (1 + drift + shock)
		wickUp := abs(shock)*0.8 + r.next()*vol*0.6
		wickDn := abs(shock)*0.8 + r.next()*vol*0.6
		high := max(open, close) * (1 + wickUp*0.5)
		low := min(open, close) * (1 - wickDn*0.5)
		candles = append(candles, market.Candle{
			Time:   t,
			Open:   round(open, m.digits),
			High:   round(high, m.digits),
			Low:    round(low, m.digits),
			Close:  round(close, m.digits),
			Volume: math.Round(m.baseVolume * r.between(0.35, 1.8)),
		})
		prevClose = close
	}

	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}
	toPoints := func(vals []float64, warmup int) []api.SeriesPoint {
		pts := make([]api.SeriesPoint, 0, len(vals)-warmup+1)
		for i := warmup - 1; i < len(vals); i++ {
			pts = append(pts, api.SeriesPoint{
				Time:  candles[i].Time.UTC().Format(time.RFC3339),
				Value: round(vals[i], m.digits),
			})
		}
		return pts
	}

	return api.CandleSeries{
		AssetID:   assetID,
		Timeframe: tf,
		Candles:   candles,
		Overlays: api.Overlays{
			EMA20: toPoints(ema(closes, 20), 20),
			EMA50: toPoints(ema(closes, 50), 50),
		},
	}, nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
