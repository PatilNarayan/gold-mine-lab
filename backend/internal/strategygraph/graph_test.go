package strategygraph

import (
	"context"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

func reg() strategy.Registry {
	r := strategy.NewRegistry()
	_ = r.Register(strategy.NewEMACross())
	_ = r.Register(strategy.NewRSIMeanReversion())
	return r
}

func src(id, sid string, p map[string]any) Node {
	return Node{ID: id, Type: NodeSource, StrategyID: sid, Parameters: p}
}
func emaRSIAnd() Definition {
	return Definition{
		Nodes: []Node{
			src("ema", "ema-cross", map[string]any{"fast_period": 5, "slow_period": 20}),
			src("rsi", "rsi-mean-reversion", nil),
			{ID: "and", Type: NodeAnd},
			{ID: "out", Type: NodeOutput},
		},
		Edges: []Edge{{"ema", "and"}, {"rsi", "and"}, {"and", "out"}},
	}
}

func TestValidate(t *testing.T) {
	r := reg()
	valid := []Definition{
		{Nodes: []Node{src("ema", "ema-cross", nil), {ID: "out", Type: NodeOutput}}, Edges: []Edge{{"ema", "out"}}},
		{Nodes: []Node{src("rsi", "rsi-mean-reversion", nil), {ID: "out", Type: NodeOutput}}, Edges: []Edge{{"rsi", "out"}}},
		emaRSIAnd(),
	}
	for i, d := range valid {
		if errs := Validate(d, r); len(errs) > 0 {
			t.Errorf("valid graph %d rejected: %v", i, errs)
		}
	}
	bad := []Definition{
		{Nodes: []Node{src("x", "nope", nil), {ID: "out", Type: NodeOutput}}, Edges: []Edge{{"x", "out"}}},                                                                                                                         // unknown strategy
		{Nodes: []Node{src("e", "ema-cross", map[string]any{"fast_period": 0}), {ID: "out", Type: NodeOutput}}, Edges: []Edge{{"e", "out"}}},                                                                                       // bad params
		{Nodes: []Node{src("a", "ema-cross", nil), src("b", "ema-cross", nil)}, Edges: []Edge{{"a", "b"}, {"b", "a"}}},                                                                                                             // cycle, no output
		{Nodes: []Node{src("a", "ema-cross", nil), {ID: "o1", Type: NodeOutput}, {ID: "o2", Type: NodeOutput}}, Edges: []Edge{{"a", "o1"}, {"a", "o2"}}},                                                                           // 2 outputs
		{Nodes: []Node{src("a", "ema-cross", nil)}, Edges: nil},                                                                                                                                                                    // no output
		{Nodes: []Node{src("a", "ema-cross", nil), src("orphan", "rsi-mean-reversion", nil), {ID: "out", Type: NodeOutput}}, Edges: []Edge{{"a", "out"}}},                                                                          // disconnected
		{Nodes: []Node{{ID: "t", Type: NodeThreshold, Threshold: f(1.5)}, src("a", "ema-cross", nil), {ID: "out", Type: NodeOutput}}, Edges: []Edge{{"a", "t"}, {"t", "out"}}},                                                     // bad threshold
		{Nodes: []Node{src("a", "ema-cross", nil), src("b", "ema-cross", nil), src("c2", "ema-cross", nil), {ID: "and", Type: NodeAnd}, {ID: "out", Type: NodeOutput}}, Edges: []Edge{{"a", "and"}, {"b", "and"}, {"and", "out"}}}, // c2 unreachable
	}
	for i, d := range bad {
		if errs := Validate(d, r); len(errs) == 0 {
			t.Errorf("bad graph %d accepted", i)
		}
	}
}

func f(v float64) *float64 { return &v }

func candles(n int, up bool) []market.Candle {
	out := make([]market.Candle, n)
	tm := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	px := 100.0
	for i := range out {
		if up {
			px *= 1.01
		} else {
			px *= 0.99
		}
		out[i] = market.Candle{Time: tm.AddDate(0, 0, i), Open: px * 0.999, High: px * 1.001, Low: px * 0.998, Close: px}
	}
	return out
}

func TestDeterministicEvaluation(t *testing.T) {
	g, err := New(reg(), emaRSIAnd(), "g1", "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	cs := candles(300, true)
	s1 := g.NewStream()
	s2 := g.NewStream()
	for i, c := range cs {
		a, b := s1.Next(c), s2.Next(c)
		if a.Action != b.Action || a.Score != b.Score {
			t.Fatalf("nondeterministic at bar %d: %+v vs %+v", i, a, b)
		}
	}
	sig, err := g.Evaluate(context.Background(), cs)
	if err != nil {
		t.Fatal(err)
	}
	last := g.NewStream()
	var ls strategy.Signal
	for _, c := range cs {
		ls = last.Next(c)
	}
	if ls.Action != sig.Action || ls.Score != sig.Score {
		t.Errorf("stream/batch mismatch: %+v vs %+v", ls, sig)
	}
}

func TestOperatorSemantics(t *testing.T) {
	buy := strategy.Signal{Action: strategy.ActionBuy, Score: 0.8}
	sell := strategy.Signal{Action: strategy.ActionSell, Score: 0.6}
	neu := strategy.Signal{Action: strategy.ActionNeutral}
	c := candles(1, true)[0]

	and := Node{ID: "and", Type: NodeAnd}
	out := map[string]strategy.Signal{"a": buy, "b": sell}
	if got := combine(and, []Edge{{"a", "and"}, {"b", "and"}}, out, c); got.Action != strategy.ActionNeutral {
		t.Error("and conflict must be NEUTRAL")
	}
	out = map[string]strategy.Signal{"a": buy, "b": {Action: strategy.ActionBuy, Score: 0.4}}
	if got := combine(and, []Edge{{"a", "and"}, {"b", "and"}}, out, c); got.Action != strategy.ActionBuy || got.Score != 0.4 {
		t.Error("and should emit BUY with min score")
	}
	out = map[string]strategy.Signal{"a": buy, "b": neu}
	if got := combine(and, []Edge{{"a", "and"}, {"b", "and"}}, out, c); got.Action != strategy.ActionNeutral {
		t.Error("and with neutral input must be NEUTRAL")
	}
	or := Node{ID: "or", Type: NodeOr}
	out = map[string]strategy.Signal{"a": buy, "b": sell}
	if got := combine(or, []Edge{{"a", "or"}, {"b", "or"}}, out, c); got.Action != strategy.ActionNeutral {
		t.Error("or conflict must be NEUTRAL")
	}
	out = map[string]strategy.Signal{"a": buy, "b": neu}
	if got := combine(or, []Edge{{"a", "or"}, {"b", "or"}}, out, c); got.Action != strategy.ActionBuy || got.Score != 0.8 {
		t.Error("or should pass actionable input")
	}
	th := Node{ID: "t", Type: NodeThreshold, Threshold: f(0.5)}
	out = map[string]strategy.Signal{"a": {Action: strategy.ActionBuy, Score: 0.3}}
	if got := combine(th, []Edge{{"a", "t"}}, out, c); got.Action != strategy.ActionNeutral {
		t.Error("below threshold must be NEUTRAL")
	}
	out = map[string]strategy.Signal{"a": {Action: strategy.ActionBuy, Score: 0.7}}
	if got := combine(th, []Edge{{"a", "t"}}, out, c); got.Action != strategy.ActionBuy {
		t.Error("at/above threshold must pass")
	}
}

func TestIndependentSourceStreams(t *testing.T) {
	d := Definition{
		Nodes: []Node{
			src("e1", "ema-cross", map[string]any{"fast_period": 5, "slow_period": 20}),
			src("e2", "ema-cross", map[string]any{"fast_period": 9, "slow_period": 40}),
			{ID: "or", Type: NodeOr}, {ID: "out", Type: NodeOutput},
		},
		Edges: []Edge{{"e1", "or"}, {"e2", "or"}, {"or", "out"}},
	}
	g, err := New(reg(), d, "g", "t", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.sources) != 2 || g.sources["e1"] == g.sources["e2"] {
		t.Fatal("source nodes must own independent strategy instances")
	}
	if g.WarmupBars() != 120 {
		t.Errorf("warmup = max(sources) = 40*3 = 120, got %d", g.WarmupBars())
	}
}
