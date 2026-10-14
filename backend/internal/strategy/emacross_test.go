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
	s := NewEMACross()
	cfg, err := s.Configure(Parameters{"fast_period": 5.0, "slow_period": 10.0})
	if err != nil {
		t.Fatal(err)
	}
	s = cfg.(*EMACross)

	// Flat for 11 bars (past the slow warm-up) → sharp rise (BUY cross) →
	// sharp fall (SELL cross). Crosses must occur *after* warm-up to count.
	closes := []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100,
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
	for i := 0; i <= 10; i++ {
		if acts[i] != ActionNeutral {
			t.Fatalf("bar %d emitted %s during warm-up", i, acts[i])
		}
	}
}

func TestEMACrossNeutralWithoutCross(t *testing.T) {
	s := NewEMACross()
	cfg, _ := s.Configure(Parameters{"fast_period": 5.0, "slow_period": 10.0})
	stream := cfg.(Streamer).NewStream()

	// Strictly rising — fast stays above slow: NEUTRAL forever (a signal
	// requires an actual cross, not merely fast > slow).
	candles := candlesFrom([]float64{90, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102, 103, 104, 105})
	for i, c := range candles {
		sig := stream.Next(c)
		if i > 10 && sig.Action != ActionNeutral {
			t.Fatalf("bar %d: expected NEUTRAL on one-way trend, got %s", i, sig.Action)
		}
	}
}

func TestEMACrossEvaluate(t *testing.T) {
	s := NewEMACross()
	cfg, _ := s.Configure(Parameters{"fast_period": 5.0, "slow_period": 10.0})
	// flat×11 then accelerating rise → cross up at the last bar.
	candles := candlesFrom([]float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 11, 13, 16})
	sig, err := cfg.Evaluate(context.Background(), candles)
	if err != nil {
		t.Fatal(err)
	}
	if sig.Timestamp.IsZero() || sig.Price == 0 {
		t.Fatal("signal missing price/timestamp")
	}
	if sig.Action == ActionBuy && sig.Reason != "EMA5 crossed above EMA10" {
		t.Fatalf("unexpected reason %q", sig.Reason)
	}
}

// Both evaluation paths must agree bar-for-bar on the same series.
func TestEMACrossStreamEqualsEvaluate(t *testing.T) {
	s := NewEMACross()
	cfg, _ := s.Configure(Parameters{"fast_period": 5.0, "slow_period": 10.0})
	closes := []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100,
		102, 106, 110, 115, 120, 126,
		121, 114, 107, 100, 93, 86, 79, 72}
	candles := candlesFrom(closes)
	stream := cfg.(Streamer).NewStream()
	for i, c := range candles {
		got := stream.Next(c)
		if i < 11 { // batch path needs slow+2 bars
			continue
		}
		want, err := cfg.Evaluate(context.Background(), candles[:i+1])
		if err != nil {
			t.Fatal(err)
		}
		if got.Action != want.Action || got.Timestamp != want.Timestamp {
			t.Fatalf("bar %d: stream %s@%v vs evaluate %s@%v",
				i, got.Action, got.Timestamp, want.Action, want.Timestamp)
		}
	}
}

func TestEMACrossConfigureValidation(t *testing.T) {
	s := NewEMACross()
	for name, p := range map[string]Parameters{
		"fast >= slow":   {"fast_period": 50.0, "slow_period": 20.0},
		"non-positive":   {"fast_period": -1.0},
		"fast below min": {"fast_period": 1.0},
		"fast above max": {"fast_period": 200.0},
		"slow below min": {"slow_period": 5.0},
		"slow above max": {"slow_period": 500.0},
		"non-integer":    {"fast_period": 20.7},
		"wrong type":     {"fast_period": "abc"},
	} {
		if _, err := s.Configure(p); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	// Boundaries pass: fast=2, slow=10.
	if _, err := s.Configure(Parameters{"fast_period": 2.0, "slow_period": 10.0}); err != nil {
		t.Errorf("boundary params rejected: %v", err)
	}
}
