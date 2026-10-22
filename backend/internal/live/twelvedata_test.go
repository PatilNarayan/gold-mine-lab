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
	p := testProvider(t, TwelveDataConfig{APIKey: "k", WSURL: wsURL, RESTBaseURL: srv.URL, Transport: "websocket", StaleAfter: time.Hour})
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

// book.replace must merge the provider's row for the forming bucket instead
// of adopting it wholesale — a reconcile issued before the newest ticks
// arrived must not regress tick-accumulated high/low/close.
func TestReplaceMergesFormingWithProviderRow(t *testing.T) {
	b := &candleBook{def: supportedTimeframes["15m"]}
	base := time.Unix(1760000100, 0).UTC().Truncate(15 * time.Minute)
	// Ticks built a forming bar with a wide range.
	b.onTick(base.Add(time.Minute), 100)
	b.onTick(base.Add(2*time.Minute), 120) // high 120
	b.onTick(base.Add(3*time.Minute), 95)  // low 95, close 95
	// Provider row for the same bucket saw none of that range.
	b.replace([]Candle{
		{Time: base.Add(-15 * time.Minute), Open: 90, High: 91, Low: 89, Close: 90},
		{Time: base, Open: 99, High: 102, Low: 98, Close: 101},
	}, time.Now())
	out := b.snapshot(10)
	if len(out) != 2 {
		t.Fatalf("snapshot = %+v", out)
	}
	f := out[1]
	if f.Open != 99 {
		t.Fatalf("provider open must win: %+v", f)
	}
	if f.High != 120 || f.Low != 95 || f.Close != 95 {
		t.Fatalf("tick-accumulated range lost: %+v", f)
	}
}

// Session books bucket on Kyiv civil boundaries and emit the platform's
// naive-label convention — the same label the CSV provider produces for
// broker wall time.
func TestSessionBucketKyivCivilDay(t *testing.T) {
	def := supportedTimeframes["1d"]
	// 2026-10-22 is still EEST (UTC+3): the Kyiv day runs 21:00Z→21:00Z.
	for _, tc := range []struct {
		ts   string
		want string
	}{
		{"2026-10-22T20:59:59Z", "2026-10-22T00:00:00Z"},
		{"2026-10-22T21:00:00Z", "2026-10-23T00:00:00Z"},
		{"2026-10-23T12:34:00Z", "2026-10-23T00:00:00Z"},
		// After the Oct-26 fall-back (EET, UTC+2): boundary moves to 22:00Z.
		{"2026-10-26T21:59:59Z", "2026-10-26T00:00:00Z"},
		{"2026-10-26T22:00:00Z", "2026-10-27T00:00:00Z"},
	} {
		ts, _ := time.Parse(time.RFC3339, tc.ts)
		got := sessionBucket(ts, def)
		if got.Format(time.RFC3339) != tc.want {
			t.Fatalf("sessionBucket(%s) = %s, want %s", tc.ts, got.Format(time.RFC3339), tc.want)
		}
	}
}

func TestSessionBucketKyiv4h(t *testing.T) {
	def := supportedTimeframes["4h"]
	// EEST (+3): Kyiv 03:30 → 4h bucket 00:00 Kyiv → label 00:00Z.
	ts, _ := time.Parse(time.RFC3339, "2026-10-22T00:30:00Z")
	if got := sessionBucket(ts, def); got.Format(time.RFC3339) != "2026-10-22T00:00:00Z" {
		t.Fatalf("4h label = %s", got.Format(time.RFC3339))
	}
	// Kyiv 05:10 → 4h bucket 04:00 Kyiv → label 04:00Z.
	ts, _ = time.Parse(time.RFC3339, "2026-10-22T02:10:00Z")
	if got := sessionBucket(ts, def); got.Format(time.RFC3339) != "2026-10-22T04:00:00Z" {
		t.Fatalf("4h label = %s", got.Format(time.RFC3339))
	}
}

func TestAggregateSessionFromSeedRows(t *testing.T) {
	def := supportedTimeframes["1d"]
	h := func(ts string, o, hi, lo, c float64) Candle {
		tm, _ := time.Parse(time.RFC3339, ts)
		return Candle{Time: tm, Open: o, High: hi, Low: lo, Close: c}
	}
	rows := []Candle{
		h("2026-10-22T20:00:00Z", 10, 11, 9, 10),  // Kyiv Oct-22 (EEST 23:00)
		h("2026-10-22T21:00:00Z", 20, 22, 19, 21), // Kyiv Oct-23 00:00
		h("2026-10-22T23:00:00Z", 30, 35, 28, 33), // Kyiv Oct-23 02:00
	}
	out := aggregateSession(rows, def)
	if len(out) != 2 {
		t.Fatalf("aggregated = %+v", out)
	}
	if out[0].Time.Format(time.RFC3339) != "2026-10-22T00:00:00Z" || out[0].Open != 10 || out[0].Close != 10 {
		t.Fatalf("first session candle wrong: %+v", out[0])
	}
	if out[1].Time.Format(time.RFC3339) != "2026-10-23T00:00:00Z" ||
		out[1].Open != 20 || out[1].High != 35 || out[1].Low != 19 || out[1].Close != 33 {
		t.Fatalf("second session candle wrong: %+v", out[1])
	}
}

// A session book rolls its forming candle on the Kyiv boundary, not the UTC
// boundary — the same rollover the historical convention implies.
func TestSessionBookRolloverKyivMidnight(t *testing.T) {
	b := &candleBook{def: supportedTimeframes["1d"]}
	b.onTick(mustRFC("2026-10-22T20:00:00Z"), 4000)
	b.onTick(mustRFC("2026-10-22T20:59:00Z"), 4005) // still Kyiv Oct-22
	b.onTick(mustRFC("2026-10-22T21:01:00Z"), 4010) // Kyiv Oct-23 00:01 → rollover
	out := b.snapshot(10)
	if len(out) != 2 {
		t.Fatalf("snapshot = %+v", out)
	}
	if out[0].Time.Format(time.RFC3339) != "2026-10-22T00:00:00Z" || out[0].High != 4005 {
		t.Fatalf("closed session candle wrong: %+v", out[0])
	}
	if out[1].Time.Format(time.RFC3339) != "2026-10-23T00:00:00Z" || out[1].Open != 4010 {
		t.Fatalf("forming session candle wrong: %+v", out[1])
	}
}

func mustRFC(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
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
	p := testProvider(t, TwelveDataConfig{APIKey: "k", WSURL: wsURL, RESTBaseURL: srv.URL, Transport: "websocket", StaleAfter: time.Hour})
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

// --- REST polling -----------------------------------------------------------

// quoteServer counts /quote hits; the handler decides what to return.
// /time_series gets a minimal valid payload so reconcile calls stay local.
func quoteServer(t *testing.T, quoteHits *atomic.Int32, respond func(n int32) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/quote":
			n := quoteHits.Add(1)
			code, body := respond(n)
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		case "/time_series":
			_, _ = w.Write([]byte(`{"meta":{"symbol":"XAU/USD","interval":"15min"},"values":[{"datetime":"2025-10-17 14:30:00","open":"1","high":"2","low":"0.5","close":"1.5"}],"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func goodQuote(ts int64, close string) (int, string) {
	return http.StatusOK, fmt.Sprintf(`{"symbol":"XAU/USD","name":"Gold Spot","currency":"USD","datetime":"2025-10-09 08:53:20","timestamp":%d,"last_quote_at":%d,"close":"%s","volume":"12345","is_market_open":true}`, ts, ts, close)
}

func TestFetchQuoteParses(t *testing.T) {
	var hits atomic.Int32
	srv := quoteServer(t, &hits, func(int32) (int, string) { return goodQuote(1760000000, "4101.25") })
	defer srv.Close()

	p := testProvider(t, TwelveDataConfig{APIKey: "k", RESTBaseURL: srv.URL})
	p.now = func() time.Time { return time.Unix(1760000000, 0) }
	q, err := p.fetchQuote(context.Background())
	if err != nil {
		t.Fatalf("fetchQuote: %v", err)
	}
	if q.Price != 4101.25 || q.Symbol != "XAU/USD" || q.AssetID != "xauusd" {
		t.Fatalf("quote = %+v", q)
	}
	if q.ProviderTime == nil || q.ProviderTime.Unix() != 1760000000 {
		t.Fatalf("providerTime = %v", q.ProviderTime)
	}
	if q.DayVolume == nil || *q.DayVolume != 12345 {
		t.Fatalf("dayVolume = %v", q.DayVolume)
	}
}

func TestFetchQuoteHonestWhenNoTimestamp(t *testing.T) {
	var hits atomic.Int32
	srv := quoteServer(t, &hits, func(int32) (int, string) {
		return http.StatusOK, `{"symbol":"XAU/USD","close":"4101.0","datetime":"2025-10-09"}`
	})
	defer srv.Close()
	p := testProvider(t, TwelveDataConfig{APIKey: "k", RESTBaseURL: srv.URL})
	q, err := p.fetchQuote(context.Background())
	if err != nil {
		t.Fatalf("fetchQuote: %v", err)
	}
	if q.ProviderTime != nil {
		t.Fatal("missing provider timestamp must stay nil, not be invented")
	}
}

func TestFetchQuoteRejectsBadPayloads(t *testing.T) {
	var hits atomic.Int32
	cases := []struct {
		name string
		body string
	}{
		{"error payload", `{"code":400,"message":"symbol not supported on plan","status":"error"}`},
		{"wrong symbol", `{"symbol":"EUR/USD","close":"1.1","timestamp":1760000000}`},
		{"bad price", `{"symbol":"XAU/USD","close":"abc","timestamp":1760000000}`},
		{"negative price", `{"symbol":"XAU/USD","close":"-5","timestamp":1760000000}`},
		{"implausible ts", `{"symbol":"XAU/USD","close":"4100","timestamp":99999999999}`},
	}
	for _, tc := range cases {
		srv := quoteServer(t, &hits, func(int32) (int, string) { return http.StatusOK, tc.body })
		p := testProvider(t, TwelveDataConfig{APIKey: "k", RESTBaseURL: srv.URL})
		if _, err := p.fetchQuote(context.Background()); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		srv.Close()
	}
}

func TestPollSuperviseCadenceAndSharedCache(t *testing.T) {
	var hits atomic.Int32
	var tsHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/quote" {
			hits.Add(1)
			_, body := goodQuote(1760000000, "4100.0")
			_, _ = w.Write([]byte(body))
			return
		}
		tsHits.Add(1)
		_, _ = w.Write([]byte(`{"status":"error","code":403,"message":"plan lacks access"}`))
	}))
	defer srv.Close()

	p := testProvider(t, TwelveDataConfig{
		APIKey: "k", RESTBaseURL: srv.URL, Transport: "rest",
		PollInterval: 60 * time.Millisecond, StaleAfter: time.Hour,
	})
	p.now = func() time.Time { return time.Unix(1760000000, 0) }
	p.Start(context.Background())

	time.Sleep(380 * time.Millisecond)
	n := hits.Load()
	if n < 3 || n > 9 {
		t.Fatalf("poll cadence wrong: %d /quote calls in ~380ms at 60ms interval", n)
	}
	q, ok := p.LatestQuote()
	if !ok || q.Price != 4100.0 {
		t.Fatalf("quote not cached: %+v ok=%v", q, ok)
	}
	st := p.Status()
	if st.State != StateConnected || st.Transport != "rest" {
		t.Fatalf("status = %+v, want connected+rest", st)
	}
	if st.LastFetchAt == nil {
		t.Fatal("LastFetchAt not set")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	after := hits.Load()
	time.Sleep(200 * time.Millisecond)
	if hits.Load() != after {
		t.Fatal("poll worker did not stop on Close")
	}
}

func TestPollRecoveryAfterFailures(t *testing.T) {
	var hits atomic.Int32
	srv := quoteServer(t, &hits, func(n int32) (int, string) {
		if n <= 2 {
			return http.StatusInternalServerError, `{"status":"error"}`
		}
		return goodQuote(1760000000, "4102.0")
	})
	defer srv.Close()

	p := testProvider(t, TwelveDataConfig{
		APIKey: "k", RESTBaseURL: srv.URL, Transport: "rest",
		PollInterval: 40 * time.Millisecond, StaleAfter: time.Hour,
	})
	p.now = func() time.Time { return time.Unix(1760000000, 0) }
	p.Start(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := p.Status(); st.State == StateConnected {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("provider never recovered: %+v", p.Status())
}

func TestPollAuthErrorParks(t *testing.T) {
	var hits atomic.Int32
	srv := quoteServer(t, &hits, func(int32) (int, string) {
		return http.StatusOK, `{"code":401,"message":"invalid api key","status":"error"}`
	})
	defer srv.Close()

	p := testProvider(t, TwelveDataConfig{
		APIKey: "k", RESTBaseURL: srv.URL, Transport: "rest",
		PollInterval: 30 * time.Millisecond,
	})
	p.Start(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p.Status().State == StateConfigError {
			before := hits.Load()
			time.Sleep(150 * time.Millisecond)
			if hits.Load() > before+1 {
				t.Fatal("kept polling after auth failure")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected config_error, got %+v", p.Status())
}

func TestPollDedupeSameTimestamp(t *testing.T) {
	var hits atomic.Int32
	var tick atomic.Int32
	srv := quoteServer(t, &hits, func(int32) (int, string) {
		// Same provider timestamp every poll; close inches up.
		n := tick.Add(1)
		return goodQuote(1760000000, fmt.Sprintf("410%d", n))
	})
	defer srv.Close()

	p := testProvider(t, TwelveDataConfig{
		APIKey: "k", RESTBaseURL: srv.URL, Transport: "rest",
		PollInterval: 30 * time.Millisecond, StaleAfter: time.Hour,
	})
	p.now = func() time.Time { return time.Unix(1760000000, 0) }
	p.Start(context.Background())
	time.Sleep(220 * time.Millisecond)

	cs, err := p.Candles(context.Background(), "15m", 10)
	if err != nil {
		t.Fatalf("Candles: %v", err)
	}
	// One forming candle at the provider bucket; close reflects the last poll.
	var forming *Candle
	for i := range cs {
		if cs[i].Time.Equal(time.Unix(1760000000, 0).Truncate(15 * time.Minute)) {
			forming = &cs[i]
		}
	}
	if forming == nil {
		t.Fatalf("no forming candle at provider bucket: %+v", cs)
	}
	last, _ := p.LatestQuote()
	if forming.Close != last.Price {
		t.Fatalf("forming close %v != latest price %v", forming.Close, last.Price)
	}
}

func TestReconcileDoesNotDuplicateFormingBucket(t *testing.T) {
	var hits atomic.Int32
	// Seed row at the forming bucket (19:15) plus one older candle.
	srv := quoteServer(t, &hits, func(int32) (int, string) { return goodQuote(1792437200, "4144.5") })
	defer srv.Close()

	p := testProvider(t, TwelveDataConfig{
		APIKey: "k", RESTBaseURL: srv.URL, Transport: "rest",
		PollInterval: 10 * time.Second, StaleAfter: time.Hour, CandleRefresh: time.Hour,
	})
	p.now = func() time.Time { return time.Unix(1792437300, 0) } // 19:15:00Z

	// Fold a tick into the 19:15 bucket, then reconcile over it with a
	// provider response that includes a row for the same bucket.
	ts := time.Unix(1792437300, 0)
	p.applyQuote(Quote{AssetID: "xauusd", Symbol: "XAU/USD", Price: 4144.5,
		ProviderTime: &ts, ReceivedAt: ts})
	book := p.books["15m"]
	book.replace([]Candle{
		{Time: ts.Truncate(15 * time.Minute).Add(-15 * time.Minute), Open: 1, High: 2, Low: .5, Close: 1.5},
		{Time: ts.Truncate(15 * time.Minute), Open: 3, High: 4, Low: 2.5, Close: 3.5},
	}, ts)

	got := book.snapshot(10)
	seen := map[time.Time]int{}
	for _, c := range got {
		seen[c.Time]++
	}
	for tm, n := range seen {
		if n > 1 {
			t.Fatalf("bucket %s emitted %d times: %+v", tm, n, got)
		}
	}
	// Merged, not adopted: provider open/bucket win, tick-accumulated
	// high/low/close survive the reconcile.
	last := got[len(got)-1]
	want := Candle{Time: ts.Truncate(15 * time.Minute), Open: 3, High: 4144.5, Low: 2.5, Close: 4144.5}
	if !last.Time.Equal(want.Time) || last.Open != want.Open || last.High != want.High ||
		last.Low != want.Low || last.Close != want.Close {
		t.Fatalf("forming bucket merge wrong: got %+v want %+v", last, want)
	}
}
