package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testTiingo(t *testing.T, cfg TiingoConfig) *Tiingo {
	t.Helper()
	p := NewTiingo(cfg)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// qFrame builds a documented fx "A" frame:
// ["Q", ticker, ISO-ts, bidSize, bidPrice, midPrice, askSize, askPrice].
func qFrame(ticker, ts string, bidSize, bid, mid, askSize, ask float64) string {
	return fmt.Sprintf(`{"service":"fx","messageType":"A","data":["Q",%q,%q,%v,%v,%v,%v,%v]}`,
		ticker, ts, bidSize, bid, mid, askSize, ask)
}

// --- onUpdate parsing/validation ----------------------------------------------

func TestTiingoAcceptsQuoteMessage(t *testing.T) {
	p := testTiingo(t, TiingoConfig{APIToken: "tok"})
	now := time.Now().UTC()
	p.now = func() time.Time { return now }

	p.onUpdate(json.RawMessage(
		`["Q","xauusd","` + now.Format(time.RFC3339Nano) + `",1000000,4100.10,4100.30,1000000,4100.50]`))

	q, ok := p.LatestQuote()
	if !ok {
		t.Fatal("quote not accepted")
	}
	if q.Price != 4100.30 || q.PriceKind != "mid" {
		t.Fatalf("price/kind = %v %q, want mid 4100.30", q.Price, q.PriceKind)
	}
	if q.Bid == nil || *q.Bid != 4100.10 || q.Ask == nil || *q.Ask != 4100.50 {
		t.Fatalf("bid/ask not preserved: %+v", q)
	}
	if q.ProviderTime == nil || !q.ProviderTime.Equal(now.Truncate(0)) {
		t.Fatalf("providerTime = %v", q.ProviderTime)
	}
	if st := p.Status(); st.State != StateConnected || st.Provider != "tiingo" || st.Transport != "websocket" {
		t.Fatalf("status = %+v", st)
	}
}

func TestTiingoRejectsWrongTickerAndMalformed(t *testing.T) {
	p := testTiingo(t, TiingoConfig{APIToken: "tok"})
	now := time.Now().UTC()
	p.now = func() time.Time { return now }
	ts := now.Format(time.RFC3339Nano)

	for _, raw := range []string{
		`["Q","eurusd","` + ts + `",1,1.10,1.105,1,1.11]`,    // unrequested symbol
		`["T","xauusd","` + ts + `",1,4100,4100.3,1,4100.5]`, // trade frame — not top-of-book
		`["Q","xauusd","not-a-time",1,4100,4100.3,1,4100.5]`,
		`["Q","xauusd","` + ts + `",1,4100,null,1,null]`,               // null mid + no ask → nothing usable
		`["Q","xauusd","` + ts + `",1,4101,4100.5,1,4100]`,             // crossed book bid>ask
		`["Q","xauusd","` + ts + `",1,-5,-5,1,-4]`,                     // negative prices
		`["Q","xauusd","2030-01-01T00:00:00Z",1,4100,4100.3,1,4100.5]`, // implausible ts
		`["Q","xauusd"]`, // truncated
		`"not-an-array"`,
	} {
		p.onUpdate(json.RawMessage(raw))
	}
	if _, ok := p.LatestQuote(); ok {
		t.Fatal("no malformed/foreign quote should have been accepted")
	}
}

func TestTiingoMidFallbackFromBidAsk(t *testing.T) {
	p := testTiingo(t, TiingoConfig{APIToken: "tok"})
	now := time.Now().UTC()
	p.now = func() time.Time { return now }

	// midPrice null → (bid+ask)/2 from a sane book.
	p.onUpdate(json.RawMessage(
		`["Q","xauusd","` + now.Format(time.RFC3339Nano) + `",1000000,4100.0,null,1000000,4100.4]`))
	q, ok := p.LatestQuote()
	if !ok || q.Price != 4100.2 {
		t.Fatalf("fallback mid = %+v ok=%v, want 4100.2", q, ok)
	}
}

func TestTiingoDropsOutOfOrder(t *testing.T) {
	p := testTiingo(t, TiingoConfig{APIToken: "tok"})
	now := time.Now().UTC()
	p.now = func() time.Time { return now }

	p.onUpdate(json.RawMessage(`["Q","xauusd","` + now.Format(time.RFC3339Nano) + `",1,4100,4100.1,1,4100.2]`))
	p.onUpdate(json.RawMessage(`["Q","xauusd","` + now.Add(-time.Second).Format(time.RFC3339Nano) + `",1,9999,9999.5,1,10000]`))

	q, _ := p.LatestQuote()
	if q.Price != 4100.1 {
		t.Fatalf("out-of-order update overwrote quote: %+v", q)
	}
}

// --- REST candle seeding ------------------------------------------------------

func TestTiingoFetchPrices(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tiingo/fx/xauusd/prices" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("resampleFreq") != "15min" {
			t.Errorf("resampleFreq = %q", r.URL.Query().Get("resampleFreq"))
		}
		if r.URL.Query().Get("token") != "" {
			t.Error("token must travel in the Authorization header, never the URL")
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[
			{"date":"2025-10-17T14:15:00","ticker":"xauusd","open":1,"high":2,"low":0.5,"close":1.5},
			{"date":"2025-10-17T14:30:00","ticker":"xauusd","open":2,"high":3,"low":1,"close":2.5,"volume":10}
		]`))
	}))
	defer srv.Close()

	p := testTiingo(t, TiingoConfig{APIToken: "sekrit-token", RESTBaseURL: srv.URL})
	cs, err := p.fetchPrices(context.Background(), "15m", 100)
	if err != nil {
		t.Fatalf("fetchPrices: %v", err)
	}
	if gotAuth != "Token sekrit-token" {
		t.Fatalf("Authorization header = %q", gotAuth)
	}
	if len(cs) != 2 || !cs[0].Time.Before(cs[1].Time) {
		t.Fatalf("expected ascending candles, got %+v", cs)
	}
	if cs[1].Volume == nil || *cs[1].Volume != 10 {
		t.Fatalf("volume not preserved: %+v", cs[1])
	}
}

func TestTiingoFetchPricesAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Invalid token"}`))
	}))
	defer srv.Close()
	p := testTiingo(t, TiingoConfig{APIToken: "k", RESTBaseURL: srv.URL})
	_, err := p.fetchPrices(context.Background(), "15m", 10)
	if err == nil || !isAuthFailure(err) {
		t.Fatalf("expected authError, got %v", err)
	}
}

func TestParseTiingoCandleValidation(t *testing.T) {
	if _, err := parseTiingoCandle(tiingoPriceRow{
		Date: "2025-10-17T14:30:00", Open: 1, High: 0.5, Low: 1, Close: 1.2,
	}); err == nil {
		t.Fatal("expected high<low rejection")
	}
	if _, err := parseTiingoCandle(tiingoPriceRow{Date: "bogus", Open: 1, High: 2, Low: 1, Close: 1.5}); err == nil {
		t.Fatal("expected bad date rejection")
	}
}

// --- lifecycle / session against a mock WS -----------------------------------

// tiingoMock accepts one WS conn, asserts the subscribe frame carries the
// token in the body (never the URL), acks it, then streams quotes.
func tiingoMock(t *testing.T, token string, msgs []string, conns *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "" {
			t.Errorf("token leaked into WS URL: %s", r.URL.RawQuery)
		}
		if conns != nil {
			conns.Add(1)
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "done")
		_, sub, err := c.Read(context.Background())
		if err != nil {
			return
		}
		var f tiingoSubscribe
		if err := json.Unmarshal(sub, &f); err != nil ||
			f.EventName != "subscribe" || f.Authorization != token ||
			len(f.EventData.Tickers) != 1 || f.EventData.Tickers[0] != "xauusd" {
			t.Errorf("bad subscribe frame: %s", sub)
			return
		}
		_ = c.Write(context.Background(), websocket.MessageText,
			[]byte(`{"data":{"subscriptionId":7},"response":{"message":"Success","code":200},"messageType":"I"}`))
		for _, m := range msgs {
			if err := c.Write(context.Background(), websocket.MessageText, []byte(m)); err != nil {
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
		_, _, _ = c.Read(context.Background()) // hold open until client leaves
	}))
}

func TestTiingoSessionStreamsQuotes(t *testing.T) {
	now := time.Now().UTC()
	srv := tiingoMock(t, "tok", []string{
		qFrame("xauusd", now.Format(time.RFC3339Nano), 1e6, 4100.10, 4100.30, 1e6, 4100.50),
		qFrame("eurusd", now.Add(time.Second).Format(time.RFC3339Nano), 1, 1.10, 1.105, 1, 1.11), // foreign — ignored
		qFrame("xauusd", now.Add(2*time.Second).Format(time.RFC3339Nano), 1e6, 4100.40, 4100.60, 1e6, 4100.80),
	}, nil)
	defer srv.Close()

	p := testTiingo(t, TiingoConfig{
		APIToken: "tok", WSURL: "ws" + strings.TrimPrefix(srv.URL, "http"),
		RESTBaseURL: srv.URL, StaleAfter: time.Hour,
	})
	p.Start(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if q, ok := p.LatestQuote(); ok && q.Price == 4100.60 {
			if st := p.Status(); st.State != StateConnected || st.Provider != "tiingo" {
				t.Fatalf("status = %+v", st)
			}
			cs, err := p.Candles(context.Background(), "1m", 10)
			if err != nil || len(cs) != 1 || cs[0].Close != 4100.60 {
				t.Fatalf("forming candle = %+v err=%v", cs, err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("quotes not observed: %+v", p.Status())
}

func TestTiingoReconnectAfterDrop(t *testing.T) {
	var conns atomic.Int32
	now := time.Now().UTC()
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
		ts := now.Add(time.Duration(n) * time.Second).Format(time.RFC3339Nano)
		for _, m := range []string{
			`{"data":{"subscriptionId":1},"response":{"message":"Success","code":200},"messageType":"I"}`,
			qFrame("xauusd", ts, 1, 2000+float64(n), 2000.5+float64(n), 1, 2001+float64(n)),
		} {
			if err := c.Write(context.Background(), websocket.MessageText, []byte(m)); err != nil {
				return
			}
		}
		// abrupt drop
	}))
	defer srv.Close()

	p := testTiingo(t, TiingoConfig{
		APIToken: "tok", WSURL: "ws" + strings.TrimPrefix(srv.URL, "http"),
		RESTBaseURL: srv.URL, StaleAfter: time.Hour,
	})
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
	if !waitPrice(2000) || !waitPrice(2001) {
		q, _ := p.LatestQuote()
		t.Fatalf("reconnect not observed; conns=%d price=%v status=%+v", conns.Load(), q.Price, p.Status())
	}
}

func TestTiingoAuthErrorParks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "bye")
		_, _, _ = c.Read(context.Background())
		_ = c.Write(context.Background(), websocket.MessageText,
			[]byte(`{"response":{"message":"Invalid token","code":401},"messageType":"E"}`))
		_, _, _ = c.Read(context.Background())
	}))
	defer srv.Close()

	p := testTiingo(t, TiingoConfig{
		APIToken: "sekrit-abc123", WSURL: "ws" + strings.TrimPrefix(srv.URL, "http"), RESTBaseURL: srv.URL,
	})
	p.Start(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := p.Status(); st.State == StateConfigError {
			if strings.Contains(st.LastError, "sekrit-abc123") {
				t.Fatalf("token leaked into error: %q", st.LastError)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected config_error, got %+v", p.Status())
}

func TestTiingoStartWithoutTokenIsConfigError(t *testing.T) {
	p := testTiingo(t, TiingoConfig{})
	p.Start(context.Background())
	st := p.Status()
	if st.State != StateConfigError || !strings.Contains(st.LastError, "TIINGO_API_TOKEN") {
		t.Fatalf("state = %q err=%q", st.State, st.LastError)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// --- FetchHistory ------------------------------------------------------------

func TestTiingoFetchHistoryPagesForward(t *testing.T) {
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("startDate")
		starts = append(starts, start)
		// Page 1 covers two UTC days; page 2 one day; page 3 empty → stop.
		switch start {
		case "2020-01-01":
			_, _ = w.Write([]byte(`[
				{"date":"2020-01-01T00:00:00","ticker":"xauusd","open":1,"high":2,"low":0.5,"close":1.5},
				{"date":"2020-01-02T00:00:00","ticker":"xauusd","open":2,"high":3,"low":1,"close":2.5}
			]`))
		case "2020-01-03":
			_, _ = w.Write([]byte(`[
				{"date":"2020-01-03T00:00:00","ticker":"xauusd","open":3,"high":4,"low":2,"close":3.5}
			]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	p := testTiingo(t, TiingoConfig{APIToken: "k", RESTBaseURL: srv.URL})
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2020, 1, 4, 0, 0, 0, 0, time.UTC)
	cs, err := p.FetchHistory(context.Background(), "1d", from, to)
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if len(cs) != 3 {
		t.Fatalf("FetchHistory = %d candles: %+v", len(cs), cs)
	}
	for i := 1; i < len(cs); i++ {
		if !cs[i].Time.After(cs[i-1].Time) {
			t.Fatalf("not ascending: %+v", cs)
		}
	}
	if len(starts) < 2 {
		t.Fatalf("expected multi-page fetch, starts=%v", starts)
	}
	// Day-advance: second page must start the day after page 1's last bar.
	if starts[1] != "2020-01-03" {
		t.Fatalf("page advance = %q, want 2020-01-03", starts[1])
	}
}

func TestTiingoFetchHistorySessionLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Date-aware mock: data only on the first page.
		if r.URL.Query().Get("startDate") != "2025-07-15" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		// 1h instants relabeled to Kyiv civil day labels: 20:00Z is Kyiv
		// 23:00 EEST (Jul-15 day), 21:00Z is Kyiv 00:00 (Jul-16 day).
		_, _ = w.Write([]byte(`[
			{"date":"2025-07-15T20:00:00","ticker":"xauusd","open":1,"high":2,"low":0.5,"close":1.5},
			{"date":"2025-07-15T21:00:00","ticker":"xauusd","open":2,"high":3,"low":1,"close":2.5}
		]`))
	}))
	defer srv.Close()
	p := testTiingo(t, TiingoConfig{APIToken: "k", RESTBaseURL: srv.URL})
	cs, err := p.FetchHistory(context.Background(), "1d",
		time.Date(2025, 7, 15, 0, 0, 0, 0, time.UTC), time.Date(2025, 7, 17, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("session labels = %+v", cs)
	}
	if cs[0].Time.Format("2006-01-02 15:04") != "2025-07-15 00:00" || cs[1].Time.Format("2006-01-02 15:04") != "2025-07-16 00:00" {
		t.Fatalf("session labels = %+v", cs)
	}
	if cs[1].Open != 2 || cs[1].Close != 2.5 || cs[1].High != 3 || cs[1].Low != 1 {
		t.Fatalf("aggregated OHLC wrong: %+v", cs[1])
	}
}
