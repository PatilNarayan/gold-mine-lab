package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/internal/tradeplan"
)

// --- stubs ---------------------------------------------------------------

type stubStrategy struct {
	id, ver    string
	tf         market.Timeframe
	sig        strategy.Signal
	gotCandles []market.Candle
}

func (s *stubStrategy) ID() string                           { return s.id }
func (s *stubStrategy) Name() string                         { return s.id }
func (s *stubStrategy) Version() string                      { return s.ver }
func (s *stubStrategy) SupportedAssets() []string            { return []string{"xauusd"} }
func (s *stubStrategy) Timeframe() market.Timeframe          { return s.tf }
func (s *stubStrategy) Parameters() []strategy.ParameterSpec { return nil }
func (s *stubStrategy) Evaluate(_ context.Context, cs []market.Candle) (strategy.Signal, error) {
	s.gotCandles = cs
	s.sig.StrategyID = s.id
	return s.sig, nil
}

type fakeRegistry struct{ strats []strategy.Strategy }

func (r *fakeRegistry) Register(s strategy.Strategy) error {
	r.strats = append(r.strats, s)
	return nil
}
func (r *fakeRegistry) Get(id string) (strategy.Strategy, bool) {
	for _, s := range r.strats {
		if s.ID() == id {
			return s, true
		}
	}
	return nil, false
}
func (r *fakeRegistry) List() []strategy.Strategy { return r.strats }

// --- fixtures ------------------------------------------------------------

func lc(t time.Time, h, l, c float64) live.Candle {
	return live.Candle{Time: t, Open: c, High: h, Low: l, Close: c}
}

// bookFor builds n completed candles spaced by tf (TR=2, mid=100, one
// confirmed swing low at `dip`) ending strictly before the current label
// bucket — so nothing in the book is forming.
func bookFor(tf market.Timeframe, strength int, dip float64) []live.Candle {
	n := 20 + strength
	var step time.Duration
	var lastEnd time.Time
	now := live.NowLabel()
	switch tf {
	case market.Timeframe1d:
		step = 24 * time.Hour
		lastEnd = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	default:
		step = tfDuration[tf]
		lastEnd = now.Truncate(step)
	}
	cs := make([]live.Candle, n)
	for i := range cs {
		t := lastEnd.Add(-time.Duration(n-i) * step)
		cs[i] = lc(t, 101, 99, 100)
	}
	d := n - 1 - strength
	cs[d].Low, cs[d].High, cs[d].Close = dip, 101, 100.5
	return cs
}

func book(strength int, dip float64) []live.Candle {
	return bookFor(market.Timeframe1h, strength, dip)
}

func sig(a strategy.Action) strategy.Signal {
	return strategy.Signal{Action: a, Price: 100, Timestamp: time.Now()}
}

func freshQuote(price float64, ask *float64, kind string) *live.Quote {
	return &live.Quote{Price: price, PriceKind: kind, Ask: ask, ReceivedAt: time.Now()}
}

func newEval(p live.Provider, strats ...strategy.Strategy) *LiveSetupEvaluator {
	reg := &fakeRegistry{}
	for _, s := range strats {
		_ = reg.Register(s)
	}
	return NewLiveSetupEvaluator(p, reg, tradeplan.DefaultConfig())
}

func p1h(cs []live.Candle, q *live.Quote) *fakeProvider {
	f := connectedProvider()
	f.candlesByTF = map[string][]live.Candle{"1h": cs}
	f.quote = q
	return f
}

// --- tests ---------------------------------------------------------------

func TestAllStrategiesEvaluatedOnOwnTimeframes(t *testing.T) {
	p := connectedProvider()
	p.candlesByTF = map[string][]live.Candle{
		"1h": bookFor("1h", 3, 95), "4h": bookFor("4h", 3, 95), "1d": bookFor("1d", 3, 95),
	}
	s1 := &stubStrategy{id: "ema-cross", ver: "1.0", tf: market.Timeframe1h, sig: sig(strategy.ActionNeutral)}
	s2 := &stubStrategy{id: "rsi-mean-reversion", ver: "1.0", tf: market.Timeframe4h, sig: sig(strategy.ActionNeutral)}
	s3 := &stubStrategy{id: "donchian-breakout", ver: "1.0", tf: market.Timeframe1d, sig: sig(strategy.ActionNeutral)}
	got := newEval(p, s1, s2, s3).Evaluate(context.Background())
	if len(got) != 3 {
		t.Fatalf("expected 3 evaluations, got %d", len(got))
	}
	wantTFs := []string{"1h", "4h", "1d"}
	for i, want := range wantTFs {
		if got[i].Timeframe != market.Timeframe(want) || got[i].Status != SetupNoSetup {
			t.Fatalf("result %d wrong: %+v", i, got[i])
		}
		if p.reqTFs[i] != want {
			t.Fatalf("strategy %d should request its own timeframe %q, asked %q", i, want, p.reqTFs[i])
		}
	}
	if len(s1.gotCandles) == 0 || len(s2.gotCandles) == 0 || len(s3.gotCandles) == 0 {
		t.Fatal("every strategy must receive completed candles")
	}
}

func TestFormingCandleNeverSignaled(t *testing.T) {
	cs := book(3, 95)
	// Append a forming bar in the CURRENT 1h bucket — it must be dropped.
	cs = append(cs, lc(live.NowLabel().Truncate(time.Hour), 200, 50, 150))
	s := &stubStrategy{id: "ema-cross", ver: "1", tf: market.Timeframe1h, sig: sig(strategy.ActionNeutral)}
	newEval(p1h(cs, nil), s).Evaluate(context.Background())
	if len(s.gotCandles) != len(cs)-1 {
		t.Fatalf("forming candle leaked into evaluation: %d of %d", len(s.gotCandles), len(cs))
	}
	if s.gotCandles[len(s.gotCandles)-1].High == 200 {
		t.Fatal("evaluate received the forming bar")
	}
}

func TestCompletedBuyProducesActionablePlan(t *testing.T) {
	s := &stubStrategy{id: "ema-cross", ver: "2.0", tf: market.Timeframe1h, sig: sig(strategy.ActionBuy)}
	got := newEval(p1h(book(3, 95), freshQuote(101, f64p(101.5), "last")), s).
		Evaluate(context.Background())[0]
	if got.Status != SetupActionable || got.Plan == nil {
		t.Fatalf("expected actionable plan, got %+v", got)
	}
	if *got.EntryPrice != 101.5 || got.EntryPriceSource != "ask" {
		t.Fatalf("entry should be the live ask, got %v (%s)", *got.EntryPrice, got.EntryPriceSource)
	}
	if got.Plan.StopLoss >= *got.EntryPrice || !(got.Plan.T1 < got.Plan.T2 && got.Plan.T2 < got.Plan.T3) {
		t.Fatalf("bad plan levels: %+v", got.Plan)
	}
	if got.RiskReward == nil || *got.RiskReward != 2 {
		t.Fatalf("riskReward should be the T2 multiple 2, got %v", got.RiskReward)
	}
	if got.StrategyVersion != "2.0" || got.SignalCandleTime == nil {
		t.Fatalf("missing provenance fields: %+v", got)
	}
}

func TestSellNeverMakesShortPlan(t *testing.T) {
	s := &stubStrategy{id: "ema-cross", ver: "1", tf: market.Timeframe1h, sig: sig(strategy.ActionSell)}
	got := newEval(p1h(book(3, 95), freshQuote(99, nil, "last")), s).
		Evaluate(context.Background())[0]
	if got.Status != SetupLongExit || got.Plan != nil || got.Direction == "SHORT" {
		t.Fatalf("SELL must be a long-exit report only: %+v", got)
	}
}

func TestPlanRejectionSurfaced(t *testing.T) {
	// Ask far below the swing low → stop resolves above entry → rejected.
	s := &stubStrategy{id: "ema-cross", ver: "1", tf: market.Timeframe1h, sig: sig(strategy.ActionBuy)}
	got := newEval(p1h(book(3, 95), freshQuote(80, f64p(80.0), "last")), s).
		Evaluate(context.Background())[0]
	if got.Status != SetupPlanRejected || got.Plan != nil || got.RejectionReason == "" {
		t.Fatalf("expected surfaced rejection, got %+v", got)
	}
}

func TestStaleQuoteNotExecutable(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	s := &stubStrategy{id: "ema-cross", ver: "1", tf: market.Timeframe1h, sig: sig(strategy.ActionBuy)}
	got := newEval(p1h(book(3, 95),
		&live.Quote{Price: 101, PriceKind: "last", ReceivedAt: old, ProviderTime: &old}), s).
		Evaluate(context.Background())[0]
	if got.Status != SetupStale {
		t.Fatalf("stale quote must not be actionable, got %s", got.Status)
	}
	if got.Plan == nil || got.EntryPriceSource != "signal_close" {
		t.Fatal("plan should still be computed on the signal close")
	}
}

func TestMidAndAskSources(t *testing.T) {
	s := &stubStrategy{id: "ema-cross", ver: "1", tf: market.Timeframe1h, sig: sig(strategy.ActionBuy)}
	got := newEval(p1h(book(3, 95), freshQuote(100.5, nil, "mid")), s).
		Evaluate(context.Background())[0]
	if got.EntryPriceSource != "mid" || *got.EntryPrice != 100.5 {
		t.Fatalf("mid must be labeled mid, never ask: %+v", got)
	}
	if got.Status != SetupActionable { // fresh mid is usable but honestly labeled
		t.Fatalf("fresh mid should still evaluate actionable, got %s", got.Status)
	}
}

func TestNoCompletedCandlesAndProviderErrors(t *testing.T) {
	onlyForming := []live.Candle{lc(live.NowLabel().Truncate(time.Hour), 101, 99, 100)}
	s := &stubStrategy{id: "ema-cross", ver: "1", tf: market.Timeframe1h, sig: sig(strategy.ActionBuy)}
	got := newEval(p1h(onlyForming, nil), s).Evaluate(context.Background())[0]
	if got.Status != SetupInsufficient {
		t.Fatalf("expected insufficient_data, got %+v", got)
	}
	p := p1h(nil, nil)
	p.err = errors.New("upstream down")
	got = newEval(p, s).Evaluate(context.Background())[0]
	if got.Status != SetupUnavailable {
		t.Fatalf("expected unavailable, got %+v", got)
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	p := p1h(book(3, 95), freshQuote(101, f64p(101.5), "last"))
	s := &stubStrategy{id: "ema-cross", ver: "1", tf: market.Timeframe1h, sig: sig(strategy.ActionBuy)}
	ev := newEval(p, s)
	a, b := ev.Evaluate(context.Background()), ev.Evaluate(context.Background())
	if len(a) != len(b) || a[0].Plan == nil || b[0].Plan == nil {
		t.Fatal("evaluation should produce a plan on both runs")
	}
	if *a[0].Plan != *b[0].Plan || a[0].Status != b[0].Status {
		t.Fatal("identical inputs must produce identical plans")
	}
}
