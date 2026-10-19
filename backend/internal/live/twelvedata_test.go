package live

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testProvider(t *testing.T, cfg TwelveDataConfig) *TwelveData {
	t.Helper()
	p := NewTwelveData(cfg)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// --- parseTDCandle ----------------------------------------------------------

func TestParseTDCandle(t *testing.T) {
	c, err := parseTDCandle("2025-10-17 14:30:00", "4100.5", "4102.0", "4099.9", "4101.2", "")
	if err != nil {
		t.Fatalf("parseTDCandle: %v", err)
	}
	if c.Open != 4100.5 || c.Close != 4101.2 || c.High != 4102.0 || c.Low != 4099.9 {
		t.Fatalf("unexpected candle %+v", c)
	}
	want := time.Date(2025, 10, 17, 14, 30, 0, 0, time.UTC)
	if !c.Time.Equal(want) {
		t.Fatalf("time = %v, want %v", c.Time, want)
	}
	if c.Volume != nil {
		t.Fatalf("volume should be nil when provider omits it, got %v", *c.Volume)
	}
}

func TestParseTDCandleVolumeAndDateOnly(t *testing.T) {
	c, err := parseTDCandle("2025-10-17", "4100", "4110", "4090", "4105", "1234.5")
	if err != nil {
		t.Fatalf("parseTDCandle: %v", err)
	}
	if c.Volume == nil || *c.Volume != 1234.5 {
		t.Fatalf("volume = %v, want 1234.5", c.Volume)
	}
}

func TestParseTDCandleRejectsBadRows(t *testing.T) {
	cases := []struct{ dt, o, h, l, c string }{
		{"not-a-date", "1", "2", "0.5", "1.5"},
		{"2025-10-17", "abc", "2", "0.5", "1.5"},
		{"2025-10-17", "-1", "2", "-2", "1"},
		{"2025-10-17", "1", "0.4", "0.5", "1"}, // high < low
	}
	for i, tc := range cases {
		if _, err := parseTDCandle(tc.dt, tc.o, tc.h, tc.l, tc.c, ""); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

// --- fetchTimeSeries ---------------------------------------------------------

func timeSeriesServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("symbol"); got != "XAU/USD" {
			t.Errorf("symbol = %q", got)
		}
		if got := r.URL.Query().Get("interval"); got != "15min" {
			t.Errorf("interval = %q", got)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestFetchTimeSeriesAscending(t *testing.T) {
	srv := timeSeriesServer(t, `{"meta":{"symbol":"XAU/USD","interval":"15min"},
		"values":[
			{"datetime":"2025-10-17 14:30:00","open":"2","high":"3","low":"1","close":"2.5","volume":"10"},
			{"datetime":"2025-10-17 14:15:00","open":"1","high":"2","low":"0.5","close":"1.5"}
		],"status":"ok"}`, http.StatusOK)
	defer srv.Close()

	p := testProvider(t, TwelveDataConfig{APIKey: "k", RESTBaseURL: srv.URL})
	cs, err := p.fetchTimeSeries(context.Background(), "15m", 100)
	if err != nil {
		t.Fatalf("fetchTimeSeries: %v", err)
	}
	if len(cs) != 2 || !cs[0].Time.Before(cs[1].Time) {
		t.Fatalf("expected 2 ascending candles, got %+v", cs)
	}
}

func TestFetchTimeSeriesProviderError(t *testing.T) {
	srv := timeSeriesServer(t, `{"code":400,"message":"symbol not found","status":"error"}`, http.StatusOK)
	defer srv.Close()
	p := testProvider(t, TwelveDataConfig{APIKey: "k", RESTBaseURL: srv.URL})
	if _, err := p.fetchTimeSeries(context.Background(), "15m", 100); err == nil {
		t.Fatal("expected provider error")
	}
}

func TestFetchTimeSeriesRateLimited(t *testing.T) {
	srv := timeSeriesServer(t, `{"code":429,"message":"too many requests","status":"error"}`, http.StatusOK)
	defer srv.Close()
	p := testProvider(t, TwelveDataConfig{APIKey: "k", RESTBaseURL: srv.URL})
	_, err := p.fetchTimeSeries(context.Background(), "15m", 100)
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected rate-limit error, got %v", err)
	}
}

// --- onPrice validation ------------------------------------------------------

func tick(sym string, ts int64, price float64) wsPrice {
	return wsPrice{Event: "price", Symbol: sym, Timestamp: ts, Price: price}
}

func TestOnPriceAcceptsAndCaches(t *testing.T) {
	p := testProvider(t, TwelveDataConfig{APIKey: "k"})
	now := time.Now()
	p.now = func() time.Time { return now }
	bid, ask := 4100.1, 4100.5
	vol := 12345.0
	p.onPrice(wsPrice{Event: "price", Symbol: "XAU/USD", Timestamp: now.Unix(),
		Price: 4100.3, Bid: &bid, Ask: &ask, DayVolume: &vol})

	q, ok := p.LatestQuote()
	if !ok || q.Price != 4100.3 {
		t.Fatalf("quote = %+v ok=%v", q, ok)
	}
	if q.Bid == nil || *q.Bid != bid || q.Ask == nil || *q.Ask != ask {
		t.Fatalf("bid/ask not preserved: %+v", q)
	}
	if q.DayVolume == nil || *q.DayVolume != vol {
		t.Fatalf("day volume not preserved: %+v", q)
	}
	if q.AssetID != "xauusd" || q.Symbol != "XAU/USD" {
		t.Fatalf("asset/symbol mapping wrong: %+v", q)
	}
}

func TestOnPriceRejectsBadTicks(t *testing.T) {
	p := testProvider(t, TwelveDataConfig{APIKey: "k"})
	now := time.Now()
	p.now = func() time.Time { return now }

	p.onPrice(tick("EUR/USD", now.Unix(), 1.1))                    // wrong symbol
	p.onPrice(tick("XAU/USD", now.Unix(), -5))                     // non-positive
	p.onPrice(tick("XAU/USD", now.Unix(), 0))                      // zero
	p.onPrice(tick("XAU/USD", 0, 4100))                            // missing ts
	p.onPrice(tick("XAU/USD", now.Add(48*time.Hour).Unix(), 4100)) // implausible

	if _, ok := p.LatestQuote(); ok {
		t.Fatal("no quote should have been accepted")
	}
}

func TestOnPriceDropsOutOfOrder(t *testing.T) {
	p := testProvider(t, TwelveDataConfig{APIKey: "k"})
	now := time.Now()
	p.now = func() time.Time { return now }

	p.onPrice(tick("XAU/USD", now.Unix(), 4100))
	p.onPrice(tick("XAU/USD", now.Add(-time.Second).Unix(), 9999)) // stale tick

	q, _ := p.LatestQuote()
	if q.Price != 4100 {
		t.Fatalf("out-of-order tick overwrote quote: %+v", q)
	}
}

// --- candleBook ---------------------------------------------------------------

func TestCandleBookFormsAndAdvances(t *testing.T) {
	b := &candleBook{def: supportedTimeframes["15m"]}
	base := time.Date(2025, 10, 17, 14, 30, 0, 0, time.UTC)

	b.onTick(base.Add(2*time.Minute), 100)
	b.onTick(base.Add(5*time.Minute), 105)  // same bucket → high
	b.onTick(base.Add(7*time.Minute), 98)   // same bucket → low
	b.onTick(base.Add(20*time.Minute), 102) // new bucket → closes first

	out := b.snapshot(10)
	if len(out) != 2 {
		t.Fatalf("expected closed + forming, got %d", len(out))
	}
	closed, forming := out[0], out[1]
	if closed.Open != 100 || closed.High != 105 || closed.Low != 98 || closed.Close != 98 {
		t.Fatalf("bad closed bar %+v", closed)
	}
	if forming.Open != 102 || forming.Close != 102 {
		t.Fatalf("bad forming bar %+v", forming)
	}
	if !closed.Time.Equal(base) || !forming.Time.Equal(base.Add(15*time.Minute)) {
		t.Fatalf("bucket boundaries wrong: %v / %v", closed.Time, forming.Time)
	}

	b.onTick(base.Add(3*time.Minute), 50) // stale tick — must not reopen closed bar
	out = b.snapshot(10)
	if out[0].Close != 98 {
		t.Fatalf("stale tick mutated closed bar: %+v", out[0])
	}
}

// --- status / lifecycle ------------------------------------------------------

func TestStartWithoutKeyIsConfigError(t *testing.T) {
	p := testProvider(t, TwelveDataConfig{})
	p.Start(context.Background())
	st := p.Status()
	if st.State != StateConfigError || st.LastError == "" {
		t.Fatalf("state = %q err=%q", st.State, st.LastError)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStatusReportsStale(t *testing.T) {
	p := testProvider(t, TwelveDataConfig{APIKey: "k", StaleAfter: time.Second})
	now := time.Now()
	p.now = func() time.Time { return now }
	p.onPrice(tick("XAU/USD", now.Unix(), 4100))

	if st := p.Status(); st.State != StateConnected {
		t.Fatalf("state = %q, want connected", st.State)
	}
	p.now = func() time.Time { return now.Add(2 * time.Second) }
	if st := p.Status(); st.State != StateStale {
		t.Fatalf("state = %q, want stale", st.State)
	}
}

// --- full WS session against a local test server -----------------------------

func TestSessionStreamsQuotes(t *testing.T) {
	// Local WS server: expects the subscribe frame, acks it, sends two prices.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "done")

		_, sub, err := c.Read(context.Background())
		if err != nil {
			return
		}
		if !strings.Contains(string(sub), `"subscribe"`) || !strings.Contains(string(sub), "XAU/USD") {
			t.Errorf("unexpected subscribe frame: %s", sub)
		}
		ack := `{"event":"subscribe-status","status":"ok","success":[{"symbol":"XAU/USD"}],"fails":[]}`
		if err := c.Write(context.Background(), websocket.MessageText, []byte(ack)); err != nil {
			return
		}
		for _, msg := range []string{
			`{"event":"price","symbol":"XAU/USD","timestamp":1760000000,"price":4100.5,"bid":4100.4,"ask":4100.6}`,
			`{"event":"price","symbol":"XAU/USD","timestamp":1760000001,"price":4101.0}`,
		} {
			if err := c.Write(context.Background(), websocket.MessageText, []byte(msg)); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		// Keep the connection open until the client leaves.
		_, _, _ = c.Read(context.Background())
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	// RESTBaseURL also points at the test server — reconcile calls must never
	// reach the real provider in tests.
	p := testProvider(t, TwelveDataConfig{APIKey: "k", WSURL: wsURL, RESTBaseURL: srv.URL, StaleAfter: time.Hour})
	now := time.Unix(1760000000, 0)
	p.now = func() time.Time { return now }

	p.Start(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	var q Quote
	for time.Now().Before(deadline) {
		if got, ok := p.LatestQuote(); ok && got.Price == 4101.0 {
			q = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if q.Price != 4101.0 {
		t.Fatalf("price = %v, want latest tick 4101.0", q.Price)
	}
	if st := p.Status(); st.State != StateConnected {
		t.Fatalf("state = %q, want connected", st.State)
	}
	// Ticks at 1760000000/1760000001 share the same 1m bucket → one forming bar.
	cs, err := p.Candles(context.Background(), "1m", 10)
	if err != nil || len(cs) != 1 {
		t.Fatalf("candles = %+v err=%v", cs, err)
	}
	if cs[0].Open != 4100.5 || cs[0].High != 4101.0 || cs[0].Close != 4101.0 {
		t.Fatalf("forming candle wrong: %+v", cs[0])
	}
	if cs[0].Volume != nil {
		t.Fatal("volume must stay nil when provider omits it")
	}
}

// Each accepted connection sends a price of 2000+conn# then drops. The
// provider must resubscribe after the drop and report the newer value.
func TestReconnectAfterDrop(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := conns.Add(1)
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "bye")
		if _, _, err := c.Read(context.Background()); err != nil {
			return
		}
		msgs := []string{
			`{"event":"subscribe-status","status":"ok","success":[{"symbol":"XAU/USD"}],"fails":[]}`,
			fmt.Sprintf(`{"event":"price","symbol":"XAU/USD","timestamp":1760000000,"price":%d}`, 2000+int(n)),
		}
		for _, m := range msgs {
			if err := c.Write(context.Background(), websocket.MessageText, []byte(m)); err != nil {
				return
			}
		}
		// Abrupt drop — client must reconnect.
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	p := testProvider(t, TwelveDataConfig{APIKey: "k", WSURL: wsURL, RESTBaseURL: srv.URL, StaleAfter: time.Hour})
	p.now = func() time.Time { return time.Unix(1760000000, 0) }
	p.Start(context.Background())

	waitPrice := func(above float64) bool {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			if q, ok := p.LatestQuote(); ok && q.Price > above {
				return true
			}
			time.Sleep(25 * time.Millisecond)
		}
		return false
	}

	if !waitPrice(2000) {
		t.Fatal("first connection price not observed")
	}
	// Any newer value proves a second session streamed after the drop —
	// reconnects are fast, so an exact-price assertion races ahead of polls.
	if !waitPrice(2001) {
		q, _ := p.LatestQuote()
		t.Fatalf("reconnect not observed; conns=%d price=%v status=%+v", conns.Load(), q.Price, p.Status())
	}
	// The server drops every connection, so the state flaps between
	// connected/disconnected — the price progression is the assertion.
}
