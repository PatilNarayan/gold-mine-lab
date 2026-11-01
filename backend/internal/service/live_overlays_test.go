package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/internal/strategy/indicators"
)

func liveBars60() []live.Candle {
	cs := make([]live.Candle, 60)
	for i := range cs {
		h := 100 + math.Sin(float64(i))*5 + float64(i%7)
		l := h - 4
		cs[i] = live.Candle{Time: time.Unix(int64(i)*3600, 0), High: h, Low: l, Close: l + 2}
	}
	return cs
}

// The overlay's Donchian values must equal what the strategy itself computes
// for the same bar — verified through the strategy's own Evaluate metadata.
func TestDonchianOverlayMatchesStrategy(t *testing.T) {
	cs := liveBars60()
	ov := liveOverlays(cs, 0)
	if len(ov.DonchianUpper) == 0 || len(ov.DonchianLower) == 0 {
		t.Fatal("donchian overlay missing")
	}
	mc := make([]market.Candle, len(cs))
	for i, c := range cs {
		mc[i] = market.Candle{Time: c.Time, Open: c.Open, High: c.High, Low: c.Low, Close: c.Close}
	}
	sig, err := strategy.NewDonchianBreakout().Evaluate(context.Background(), mc)
	if err != nil {
		t.Fatalf("strategy evaluate: %v", err)
	}
	upper, _ := sig.Metadata["upper_channel"].(float64)
	lower, _ := sig.Metadata["lower_channel"].(float64)
	lastU := ov.DonchianUpper[len(ov.DonchianUpper)-1].Value
	lastL := ov.DonchianLower[len(ov.DonchianLower)-1].Value
	if math.Abs(lastU-upper) > 0.011 || math.Abs(lastL-lower) > 0.011 {
		t.Fatalf("overlay last channel (%v,%v) != strategy (%v,%v)", lastU, lastL, upper, lower)
	}
	// Warmup: lookback=20 → first 20 bars emit no channel points.
	if len(ov.DonchianUpper) != len(cs)-20 {
		t.Fatalf("expected %d channel points, got %d", len(cs)-20, len(ov.DonchianUpper))
	}
}

func TestRSIOverlayMatchesIndicators(t *testing.T) {
	cs := liveBars60()
	closes := make([]float64, len(cs))
	for i, c := range cs {
		closes[i] = c.Close
	}
	ov := liveOverlays(cs, 0)
	ref := indicators.RSI(closes, 14)
	last := ov.RSI14[len(ov.RSI14)-1].Value
	if math.Abs(last-ref[len(ref)-1]) > 0.011 {
		t.Fatalf("overlay RSI %v vs indicators.RSI %v", last, ref[len(ref)-1])
	}
	// RSI(14) is defined after 14 changes → 60-14 defined points, no zero-fill.
	if len(ov.RSI14) != len(cs)-14 {
		t.Fatalf("rsi14 should have %d points, got %d", len(cs)-14, len(ov.RSI14))
	}
}

// Overlay point timestamps must align with their source candles.
func TestOverlayTimeAlignment(t *testing.T) {
	cs := liveBars60()
	ov := liveOverlays(cs, 0)
	if got := ov.DonchianUpper[len(ov.DonchianUpper)-1].Time; got != cs[59].Time.UTC().Format(time.RFC3339) {
		t.Fatalf("last channel point misaligned: %v", got)
	}
	if got := ov.RSI14[0].Time; got != cs[14].Time.UTC().Format(time.RFC3339) {
		t.Fatalf("first RSI point should sit on candle 14, got %v", got)
	}
}
