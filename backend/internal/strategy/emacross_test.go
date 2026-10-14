package strategy

import (
	"context"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
)

// candlesFrom builds ascending candles from closes (open=close for simplicity).
func candlesFrom(closes []float64) []market.Candle {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]market.Candle, len(closes))
	for i, c := range closes {
		out[i] = market.Candle{Time: base.Add(time.Duration(i) * time.Hour), Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	return out
}

func TestEMACrossSignals(t *testing.T) {
	// fast=2, slow=3 so a handful of bars produces real crosses.
	s := NewEMACross()
	cfg, err := s.Configure(Parameters{"fast_period": 2.0, "slow_period": 3.0})
	if err != nil {
		t.Fatal(err)
	}
	s = cfg.(*EMACross)

	// Flat (fast == slow, past warm-up) → sharp rise (BUY cross) → sharp
	// fall (SELL cross). Crosses must occur *after* warm-up to count.
	closes := []float64{100, 100, 100, 100, 100, 100,
		102, 106, 110, 115, 120, 126,
		121, 114, 107, 100, 93, 86, 79, 72}
	candles := candlesFrom(closes)

	var acts []Action
	stream := s.NewStream()
	for _, c := range candles {
		sig := stream.Next(c)
		acts = append(acts, sig.Action)
	}

	buys, sells := 0, 0
	for _, a := range acts {
		if a == ActionBuy {
			buys++
		}
		if a == ActionSell {
			sells++
		}
	}
	if buys < 1 || sells < 1 {
		t.Fatalf("expected at least one BUY and one SELL, got buys=%d sells=%d", buys, sells)
	}
	// Warm-up: first `slow` bars must be NEUTRAL.
	for i := 0; i <= 3; i++ {
		if acts[i] != ActionNeutral {
			t.Fatalf("bar %d emitted %s during warm-up", i, acts[i])
		}
	}
}

func TestEMACrossNeutralWithoutCross(t *testing.T) {
	s := NewEMACross()
	cfg, _ := s.Configure(Parameters{"fast_period": 2.0, "slow_period": 3.0})
	stream := cfg.(Streamer).NewStream()

	// Strictly rising — fast stays above slow: NEUTRAL forever (a signal
	// requires an actual cross, not merely fast > slow).
	candles := candlesFrom([]float64{90, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102, 103})
	for i, c := range candles {
		sig := stream.Next(c)
		if i > 3 && sig.Action != ActionNeutral {
			t.Fatalf("bar %d: expected NEUTRAL on one-way trend, got %s", i, sig.Action)
		}
	}
}

func TestEMACrossEvaluate(t *testing.T) {
	s := NewEMACross()
	cfg, _ := s.Configure(Parameters{"fast_period": 2.0, "slow_period": 3.0})
	candles := candlesFrom([]float64{10, 10, 10, 10, 12, 14, 16, 18}) // accelerating up → cross
	sig, err := cfg.Evaluate(context.Background(), candles)
	if err != nil {
		t.Fatal(err)
	}
	if sig.Timestamp.IsZero() || sig.Price == 0 {
		t.Fatal("signal missing price/timestamp")
	}
	if sig.Action == ActionBuy && sig.Reason != "EMA2 crossed above EMA3" {
		t.Fatalf("unexpected reason %q", sig.Reason)
	}
}

func TestEMACrossConfigureValidation(t *testing.T) {
	s := NewEMACross()
	if _, err := s.Configure(Parameters{"fast_period": 50.0, "slow_period": 20.0}); err == nil {
		t.Fatal("fast >= slow must fail")
	}
	if _, err := s.Configure(Parameters{"fast_period": -1.0}); err == nil {
		t.Fatal("non-positive period must fail")
	}
}
