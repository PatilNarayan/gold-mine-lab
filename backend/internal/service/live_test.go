package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/live"
)

type fakeProvider struct {
	status  live.Status
	quote   *live.Quote
	candles []live.Candle
	err     error
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

func connectedProvider() *fakeProvider {
	return &fakeProvider{status: live.Status{
		Enabled: true, State: live.StateConnected, Provider: "twelvedata",
		Symbol: "XAU/USD", StaleAfter: 30 * time.Second,
	}}
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
	p.quote = &live.Quote{
		AssetID: "xauusd", Symbol: "XAU/USD", Price: 4100.5,
		ProviderTime: time.Now().Add(-time.Second), ReceivedAt: time.Now().Add(-time.Second),
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
