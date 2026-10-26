package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/strategy/indicators"
)

type fakeProvider struct {
	status    live.Status
	quote     *live.Quote
	candles   []live.Candle
	err       error
	listeners []func(live.Quote)
}

func (f *fakeProvider) Start(context.Context) {}
func (f *fakeProvider) Close() error          { return nil }
func (f *fakeProvider) Status() live.Status   { return f.status }
func (f *fakeProvider) LatestQuote() (live.Quote, bool) {
	if f.quote == nil {
		return live.Quote{}, false
	}
	return *f.quote, true
}
func (f *fakeProvider) Candles(context.Context, string, int) ([]live.Candle, error) {
	return f.candles, f.err
}
func (f *fakeProvider) FetchHistory(context.Context, string, time.Time, time.Time) ([]live.Candle, error) {
	return nil, nil
}
func (f *fakeProvider) FetchHistoryPages(_ context.Context, _ string, _, _ time.Time, onPage live.PageFunc) error {
	return onPage(nil, time.Time{}, time.Time{})
}
func (f *fakeProvider) HistoryFloor() time.Time { return time.Time{} }

func (f *fakeProvider) SubscribeQuotes(fn func(live.Quote)) func() {
	f.listeners = append(f.listeners, fn)
	return func() {}
}

// push delivers a quote to every registered listener — stands in for a real
// provider tick in stream tests.
func (f *fakeProvider) push(q live.Quote) {
	for _, fn := range f.listeners {
		fn(q)
	}
}

func connectedProvider() *fakeProvider {
	return &fakeProvider{status: live.Status{
		Enabled: true, State: live.StateConnected, Provider: "twelvedata",
		Symbol: "XAU/USD", StaleAfter: 30 * time.Second,
	}}
}

func TestLiveStreamBroadcastsTicks(t *testing.T) {
	fp := connectedProvider()
	fp.candles = []live.Candle{{
		Time: time.Unix(1760000000, 0).UTC(), Open: 1, High: 2, Low: 0.5, Close: 1.5,
	}}
	s := NewLiveService(fp)
	events, unsub, err := s.LiveStream(context.Background())
	if err != nil {
		t.Fatalf("LiveStream: %v", err)
	}
	defer unsub()

	// Snapshot arrives first: status frame.
	got := <-events
	if got.Type != "status" || got.Status == nil || got.Status.State != "connected" {
		t.Fatalf("first frame = %+v", got)
	}

	fp.push(live.Quote{
		AssetID: "xauusd", Symbol: "XAU/USD", Price: 4100.5, PriceKind: "last",
		ReceivedAt: time.Now(),
	})

	// Expect a quote frame then a bars frame (order from the service).
	var quote, bars *api.LiveStreamEvent
	for i := 0; i < 4 && (quote == nil || bars == nil); i++ {
		select {
		case ev := <-events:
			switch ev.Type {
			case "quote":
				quote = &ev
			case "bars":
				bars = &ev
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for stream frames")
		}
	}
	if quote == nil || quote.Quote == nil || quote.Quote.Price != 4100.5 {
		t.Fatalf("quote frame = %+v", quote)
	}
	if bars == nil || len(bars.Updates) == 0 {
		t.Fatalf("bars frame = %+v", bars)
	}
	for _, u := range bars.Updates {
		if u.Candle == nil {
			t.Fatalf("bars update missing candle: %+v", u)
		}
	}
}

func TestLiveStreamStatusOnStateChange(t *testing.T) {
	fp := connectedProvider()
	s := NewLiveService(fp)
	events, unsub, err := s.LiveStream(context.Background())
	if err != nil {
		t.Fatalf("LiveStream: %v", err)
	}
	defer unsub()
	<-events // initial status

	fp.status.State = live.StateStale
	fp.push(live.Quote{AssetID: "xauusd", Price: 1, ReceivedAt: time.Now()})

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		select {
		case ev := <-events:
			if ev.Type == "status" && ev.Status != nil && ev.Status.State == "stale" {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("no status frame on state transition")
}

func TestLiveCandlesOverlayUsesFullBook(t *testing.T) {
	// 60 candles; request only the last 10 — the overlay must compute EMA over
	// all 60 (matching the historical convention) and emit 10 aligned points.
	fp := connectedProvider()
	base := time.Unix(1760000000, 0).UTC()
	for i := 0; i < 60; i++ {
		fp.candles = append(fp.candles, live.Candle{
			Time: base.Add(time.Duration(i) * time.Minute),
			Open: float64(100 + i), High: float64(101 + i), Low: float64(99 + i), Close: float64(100 + i),
		})
	}
	s := NewLiveService(fp)
	out, err := s.LiveCandles(context.Background(), "xauusd", "15m", 10)
	if err != nil {
		t.Fatalf("LiveCandles: %v", err)
	}
	if len(out.Candles) != 10 {
		t.Fatalf("candles = %d, want 10", len(out.Candles))
	}
	if len(out.Overlays.EMA20) != 10 || len(out.Overlays.EMA50) != 10 {
		t.Fatalf("overlay lens = %d/%d, want 10/10", len(out.Overlays.EMA20), len(out.Overlays.EMA50))
	}
	// Last EMA20 point must equal EMA seeded at the FIRST book candle — not a
	// re-seed at index 50 (the visible window start).
	closes := make([]float64, 60)
	for i := range closes {
		closes[i] = float64(100 + i)
	}
	want := round2(indicators.EMA(closes, 20)[59])
	if out.Overlays.EMA20[9].Value != want {
		t.Fatalf("ema20 last = %v, want %v (full-book warmup)", out.Overlays.EMA20[9].Value, want)
	}
	if out.Overlays.EMA20[0].Time != out.Candles[0].Time {
		t.Fatalf("overlay/candle misaligned: %s vs %s", out.Overlays.EMA20[0].Time, out.Candles[0].Time)
	}
}

func TestLiveStreamTwoSubscribers(t *testing.T) {
	fp := connectedProvider()
	s := NewLiveService(fp)
	ch1, unsub1, _ := s.LiveStream(context.Background())
	ch2, unsub2, _ := s.LiveStream(context.Background())
	defer unsub1()
	defer unsub2()
	<-ch1
	<-ch2 // initial status frames

	fp.push(live.Quote{AssetID: "xauusd", Price: 42, ReceivedAt: time.Now()})
	got1, got2 := <-ch1, <-ch2
	if got1.Type != "quote" || got2.Type != "quote" || got1.Quote.Price != got2.Quote.Price {
		t.Fatalf("subscribers diverged: %+v / %+v", got1, got2)
	}
}

func TestLiveStatusDisabled(t *testing.T) {
	s := NewLiveService(nil)
	st := s.LiveStatus(context.Background())
	if st.Enabled || st.State != string(live.StateDisabled) {
		t.Fatalf("disabled status wrong: %+v", st)
	}
}

func TestLiveQuoteRejectsNonXAUUSD(t *testing.T) {
	s := NewLiveService(connectedProvider())
	for _, ep := range []func() error{
		func() error { _, err := s.LiveQuote(context.Background(), "tsla"); return err },
		func() error { _, err := s.LiveCandles(context.Background(), "msft", "1h", 10); return err },
	} {
		err := ep()
		var ve *api.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError (400), got %v", err)
		}
	}
}

func TestLiveQuoteDisabledIsUnavailable(t *testing.T) {
	s := NewLiveService(nil)
	_, err := s.LiveQuote(context.Background(), "xauusd")
	if !errors.Is(err, api.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable (503), got %v", err)
	}
}

func TestLiveQuoteNoDataYet(t *testing.T) {
	s := NewLiveService(connectedProvider()) // no quote yet
	_, err := s.LiveQuote(context.Background(), "xauusd")
	if !errors.Is(err, api.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestLiveQuoteFreshAndStale(t *testing.T) {
	p := connectedProvider()
	pt := time.Now().Add(-time.Second)
	p.quote = &live.Quote{
		AssetID: "xauusd", Symbol: "XAU/USD", Price: 4100.5,
		ProviderTime: &pt, ReceivedAt: pt,
	}
	s := NewLiveService(p)
	q, err := s.LiveQuote(context.Background(), "xauusd")
	if err != nil {
		t.Fatalf("LiveQuote: %v", err)
	}
	if q.Status != "fresh" || q.Price != 4100.5 || q.Symbol != "XAU/USD" {
		t.Fatalf("unexpected quote %+v", q)
	}
	if q.Bid != nil || q.Ask != nil {
		t.Fatal("bid/ask must stay null when provider omits them")
	}

	p.quote.ReceivedAt = time.Now().Add(-time.Minute)
	q, err = s.LiveQuote(context.Background(), "xauusd")
	if err != nil || q.Status != "stale" {
		t.Fatalf("expected stale quote, got %+v err=%v", q, err)
	}
}

func TestLiveCandlesShapesResponse(t *testing.T) {
	p := connectedProvider()
	vol := 42.0
	p.candles = []live.Candle{
		{Time: time.Date(2025, 10, 17, 10, 0, 0, 0, time.UTC), Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: &vol},
		{Time: time.Date(2025, 10, 17, 11, 0, 0, 0, time.UTC), Open: 1.5, High: 3, Low: 1, Close: 2.5},
	}
	s := NewLiveService(p)
	out, err := s.LiveCandles(context.Background(), "xauusd", "1h", 100)
	if err != nil {
		t.Fatalf("LiveCandles: %v", err)
	}
	if len(out.Candles) != 2 || out.Provider != "twelvedata" || out.Symbol != "XAU/USD" {
		t.Fatalf("unexpected response %+v", out)
	}
	if out.Candles[0].Volume == nil || *out.Candles[0].Volume != 42 {
		t.Fatal("volume not preserved")
	}
	if out.Candles[1].Volume != nil {
		t.Fatal("absent provider volume must serialize as null")
	}
}

func TestLiveCandlesBadTimeframe(t *testing.T) {
	s := NewLiveService(connectedProvider())
	_, err := s.LiveCandles(context.Background(), "xauusd", "3h", 100)
	var ve *api.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}
