package strategy

import (
	"context"
	"testing"
)

// A dip-and-recover series: RSI sinks below oversold then climbs back —
// exactly one BUY when it recrosses upward; then a run to overbought and
// a fall — one SELL on the downward cross.
func TestRSIMeanRevSignals(t *testing.T) {
	s := NewRSIMeanReversion()
	cfg, err := s.Configure(Parameters{"rsi_period": 3.0, "oversold": 25.0, "overbought": 75.0})
	if err != nil {
		t.Fatal(err)
	}
	strat := cfg.(*RSIMeanReversion)

	// Fall hard (RSI < 25), rebound across it (BUY), keep climbing past 75,
	// then fall back below (SELL). Wilder RSI(3) after a sustained run sits
	// near the extremes, so modest counter-moves cross the thresholds.
	closes := []float64{
		100, 96, 92, 88, 84, 80, // falling — RSI sinks
		84, 89, 95, 101, 108, 115, 122, 130, 138, // rising — RSI climbs
		133, 125, 118, 112, // falling again
	}
	candles := candlesFrom(closes)

	stream := strat.NewStream()
	var acts []Action
	for _, c := range candles {
		acts = append(acts, stream.Next(c).Action)
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
	if buys != 1 || sells != 1 {
		t.Fatalf("expected exactly 1 BUY and 1 SELL cross, got buys=%d sells=%d", buys, sells)
	}
	// First period+1 bars are warm-up → must be NEUTRAL.
	for i := 0; i <= 4; i++ {
		if acts[i] != ActionNeutral {
			t.Fatalf("bar %d emitted %s during warm-up", i, acts[i])
		}
	}
}

// Staying oversold must NOT repeat BUY — only the crossing bar signals.
func TestRSINoRepeatWhileBeyondThreshold(t *testing.T) {
	s := NewRSIMeanReversion()
	cfg, _ := s.Configure(Parameters{"rsi_period": 3.0})
	strat := cfg.(*RSIMeanReversion)
	// Long steady decline: RSI stays ≈0 the whole way — never crosses up.
	closes := []float64{100, 97, 94, 91, 88, 85, 82, 79, 76, 73, 70, 67}
	stream := strat.NewStream()
	for _, c := range candlesFrom(closes) {
		if sig := stream.Next(c); sig.Action != ActionNeutral {
			t.Fatalf("persistent RSI<oversold must not signal, got %s at %v", sig.Action, c.Time)
		}
	}
}

func TestRSIConfigureValidation(t *testing.T) {
	s := NewRSIMeanReversion()
	for name, p := range map[string]Parameters{
		"period below min":   {"rsi_period": 1.0},
		"period above max":   {"rsi_period": 200.0},
		"period non-int":     {"rsi_period": 14.5},
		"oversold too high":  {"oversold": 50.0},
		"overbought too low": {"overbought": 50.0},
		"os >= ob":           {"oversold": 48.0, "overbought": 51.0 - 4.0}, // 48 vs 47
		"wrong type":         {"rsi_period": "fast"},
	} {
		if _, err := s.Configure(p); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	if _, err := s.Configure(Parameters{"rsi_period": 2.0, "oversold": 20.0, "overbought": 80.0}); err != nil {
		t.Errorf("valid params rejected: %v", err)
	}
}

// Batch Evaluate and the engine's stream path must agree bar-for-bar.
func TestRSIStreamEqualsEvaluate(t *testing.T) {
	s := NewRSIMeanReversion()
	cfg, _ := s.Configure(Parameters{"rsi_period": 3.0, "oversold": 25.0, "overbought": 75.0})
	closes := []float64{
		100, 96, 92, 88, 84, 80, 84, 89, 95, 101, 108, 115, 122, 130, 138,
		133, 125, 118, 112,
	}
	candles := candlesFrom(closes)
	stream := cfg.(Streamer).NewStream()
	for i, c := range candles {
		got := stream.Next(c)
		if i < 5 { // batch path needs period+2 bars
			continue
		}
		want, err := cfg.Evaluate(context.Background(), candles[:i+1])
		if err != nil {
			t.Fatal(err)
		}
		if got.Action != want.Action {
			t.Fatalf("bar %d: stream %s vs evaluate %s", i, got.Action, want.Action)
		}
	}
}
