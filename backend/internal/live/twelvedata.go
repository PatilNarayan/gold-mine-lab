// Package live — Twelve Data provider implementation.
//
// One managed WebSocket subscription streams XAU/USD tick prices into a
// quote cache and per-timeframe forming candles. REST /time_series supplies
// recent candles on connect and on bounded reconciles (disconnect recovery,
// stale cache). All upstream communication lives here; the API key never
// leaves this process.
//
// Verified against https://twelvedata.com/docs and the WebSocket guide:
//   - WS: wss://ws.twelvedata.com/v1/quotes/price?apikey=KEY
//   - subscribe: {"action":"subscribe","params":{"symbols":"XAU/USD"}}
//   - heartbeat: {"action":"heartbeat"} every ~10s
//   - events: "subscribe-status" (success[]/fails[]), "price"
//     {symbol,timestamp(unix s),price,bid?,ask?,day_volume?}
//   - REST: GET /time_series?symbol=XAU/USD&interval=15min&outputsize=N
//     returns values newest-first; errors are {"code","message","status":"error"}
package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// TwelveDataConfig configures the managed Twelve Data provider.
type TwelveDataConfig struct {
	APIKey        string        // TWELVE_DATA_API_KEY — required when enabled
	Transport     string        // "rest" (default) or "websocket"
	PollInterval  time.Duration // REST poll cadence — default 10s
	RESTBaseURL   string        // default https://api.twelvedata.com
	WSURL         string        // default wss://ws.twelvedata.com/v1/quotes/price
	Symbol        string        // provider symbol — default "XAU/USD"
	AssetID       string        // canonical asset id reported back — default "xauusd"
	StaleAfter    time.Duration // quote age after which state reports "stale"
	CandleRefresh time.Duration // per-timeframe REST reconcile TTL — default 5m
	HTTPClient    *http.Client
	Logger        *slog.Logger
}

// intervalDef maps an app timeframe to each provider's interval parameter and
// the bar length used for forming candles.
type intervalDef struct {
	tdInterval string        // Twelve Data "interval" param (seed interval when aggregate)
	tiFreq     string        // Tiingo "resampleFreq" param (seed interval when aggregate)
	bar        time.Duration // bucket length for forming candles
	aggregate  bool          // seed 1h rows re-bucketed into session candles (4h, 1d)
}

// seedBar is the REST bar duration to fetch for this book — the book's own
// interval for 1m–1h (rows relabeled 1:1), 1h rows for session books that
// must be re-bucketed from finer provider data.
func (d intervalDef) seedBar() time.Duration {
	if d.aggregate {
		return time.Hour
	}
	return d.bar
}

// seedRows is how many seed-interval rows to fetch per reconcile. Aggregated
// books need ~24 1h rows per daily candle, so they pull a larger window.
func (d intervalDef) seedRows() int {
	if d.aggregate {
		return sessionSeedRows
	}
	return candleBookSize
}

// supportedTimeframes bounds what the live feed exposes. Twelve Data
// intervals: 1min 5min 15min 30min 45min 1h 2h 4h 8h 1day 1week 1month.
// Tiingo resampleFreq: 1min 5min 15min 30min 1hour 4hour 1day.
//
// All books bucket on the platform's market-session convention — MT5 broker
// civil time (Europe/Kyiv, EET/EEST) rendered as naive labels, matching the
// historical CSV dataset exactly. Kyiv's UTC offset is always a whole number
// of hours, so sub-hour and hourly bucket boundaries coincide across
// conventions: intraday provider rows are simply relabeled. Provider-native
// 4h/1d rows are UTC-bucketed and structurally incompatible — those books
// seed from the provider's 1h series and aggregate into Kyiv civil buckets.
var supportedTimeframes = map[string]intervalDef{
	"1m":  {"1min", "1min", time.Minute, false},
	"5m":  {"5min", "5min", 5 * time.Minute, false},
	"15m": {"15min", "15min", 15 * time.Minute, false},
	"1h":  {"1h", "1hour", time.Hour, false},
	"4h":  {"1h", "1hour", 4 * time.Hour, true},
	"1d":  {"1h", "1hour", 24 * time.Hour, true},
}

const (
	candleBookSize     = 500              // max candles retained per timeframe
	sessionSeedRows    = 2500             // 1h seed rows per session-book fetch (~100d)
	minFetchGap        = 30 * time.Second // min spacing between REST reconciles
	cacheRefreshAfter  = 5 * time.Minute  // default per-timeframe REST refresh TTL
	maxReconnectDelay  = 60 * time.Second
	initReconnectDelay = time.Second
	heartbeatInterval  = 10 * time.Second // provider-recommended cadence
)

// candleBook is one timeframe's recent history plus the forming bar that
// streamed quotes update in place.
type candleBook struct {
	mu       sync.Mutex
	def      intervalDef
	candles  []Candle // ascending, closed bars only
	forming  *Candle  // current bucket; appended to candles when a newer bucket arrives
	fetched  time.Time
	fetching bool
	lastTry  time.Time
}

// snapshot returns history + forming merged, tail-bounded.
func (b *candleBook) snapshot(limit int) []Candle {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.candles)
	if b.forming != nil {
		n++
	}
	start := 0
	if n > limit {
		start = n - limit
	}
	out := make([]Candle, 0, min(n, limit))
	for _, c := range b.candles {
		if start > 0 {
			start--
			continue
		}
		out = append(out, c)
	}
	if b.forming != nil {
		out = append(out, *b.forming)
	}
	return out
}

// bucket maps a quote timestamp to this book's candle label — the Kyiv
// civil-session boundary, matching the CSV provider's naive-label convention
// for every timeframe.
func (b *candleBook) bucket(ts time.Time) time.Time {
	return sessionBucket(ts, b.def)
}

// onTick folds a validated quote into the forming candle. Ticks that map to
// an older bucket are ignored (out-of-order protection happens upstream too).
func (b *candleBook) onTick(ts time.Time, price float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bucket := b.bucket(ts)
	if b.forming != nil {
		switch {
		case bucket.Equal(b.forming.Time):
			if price > b.forming.High {
				b.forming.High = price
			}
			if price < b.forming.Low {
				b.forming.Low = price
			}
			b.forming.Close = price
			return
		case bucket.Before(b.forming.Time):
			return // stale tick for a closed bar
		default:
			b.candles = append(b.candles, *b.forming)
			if len(b.candles) > candleBookSize {
				b.candles = b.candles[len(b.candles)-candleBookSize:]
			}
		}
	}
	b.forming = &Candle{Time: bucket, Open: price, High: price, Low: price, Close: price}
}

// replace swaps the book's history with provider candles (reconcile path).
// The forming bar is kept unless the fetch already covers its bucket.
func (b *candleBook) replace(cs []Candle, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Labels beyond the current session bucket are impossible — the provider
	// cannot report a bar that hasn't started. Guard in label space (not
	// real time): naive labels run ahead of UTC instants.
	nowLabel := sessionBucket(at, b.def)
	if n := sort.Search(len(cs), func(i int) bool { return cs[i].Time.After(nowLabel) }); n < len(cs) {
		cs = cs[:n]
	}
	if b.forming != nil {
		for i := len(cs) - 1; i >= 0; i-- {
			if cs[i].Time.Equal(b.forming.Time) {
				// Provider row for the forming bucket: merge rather than adopt —
				// the fetch was issued before the newest ticks arrived, so
				// tick-accumulated high/low/close are fresher than the row.
				// The provider's open stays authoritative for the bucket.
				merged := cs[i]
				if b.forming.High > merged.High {
					merged.High = b.forming.High
				}
				if b.forming.Low < merged.Low {
					merged.Low = b.forming.Low
				}
				merged.Close = b.forming.Close
				b.forming = &merged
				cs = append(cs[:i:i], cs[i+1:]...)
				break
			}
		}
	}
	if b.forming != nil && len(cs) > 0 && cs[len(cs)-1].Time.After(b.forming.Time) {
		// The fetch saw buckets newer than the forming bar — it is closed;
		// reinsert in order so snapshots stay strictly monotone.
		f := *b.forming
		b.forming = nil
		pos := sort.Search(len(cs), func(i int) bool { return !cs[i].Time.Before(f.Time) })
		if pos < len(cs) && cs[pos].Time.Equal(f.Time) {
			cs[pos] = f
		} else {
			cs = slices.Insert(cs, pos, f)
		}
	}
	if len(cs) > candleBookSize {
		cs = cs[len(cs)-candleBookSize:]
	}
	if b.forming == nil && len(cs) > 0 {
		// The seed tail is the still-forming interval (providers return the
		// partial current bar). Promote it so the next tick folds into it —
		// otherwise a duplicate label would appear in the snapshot.
		b.forming = &cs[len(cs)-1]
		cs = cs[:len(cs)-1]
	}
	b.candles = cs
	b.fetched = at
}

// --- ws wire messages -------------------------------------------------------

type wsSubscribe struct {
	Action string `json:"action"`
	Params struct {
		Symbols string `json:"symbols"`
	} `json:"params"`
}

type wsSubscribeStatus struct {
	Event   string `json:"event"`
	Status  string `json:"status"`
	Success []struct {
		Symbol string `json:"symbol"`
	} `json:"success"`
	Fails []struct {
		Symbol string `json:"symbol"`
	} `json:"fails"`
}

type wsPrice struct {
	Event     string   `json:"event"`
	Symbol    string   `json:"symbol"`
	Timestamp int64    `json:"timestamp"`
	Price     float64  `json:"price"`
	Bid       *float64 `json:"bid"`
	Ask       *float64 `json:"ask"`
	DayVolume *float64 `json:"day_volume"`
}

// --- REST wire messages -----------------------------------------------------

type tdTimeSeries struct {
	Meta struct {
		Symbol   string `json:"symbol"`
		Interval string `json:"interval"`
	} `json:"meta"`
	Values []struct {
		Datetime string `json:"datetime"`
		Open     string `json:"open"`
		High     string `json:"high"`
		Low      string `json:"low"`
		Close    string `json:"close"`
		Volume   string `json:"volume"`
	} `json:"values"`
	Status  string `json:"status"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// TwelveData is a managed Twelve Data provider: one WS subscription feeding
// quote + candle caches, with bounded REST reconciles.
type TwelveData struct {
	cfg  TwelveDataConfig
	log  *slog.Logger
	http *http.Client
	dial func(ctx context.Context, url string) (*websocket.Conn, *http.Response, error)
	now  func() time.Time

	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
	started bool

	mu        sync.Mutex
	state     State
	stateSubs []func(prev, next State, detail string) // transition listeners
	lastErr   string
	quote     *Quote
	lastFetch time.Time // last successful upstream fetch
	lastTick  time.Time // newest accepted provider timestamp
	books     map[string]*candleBook
	rest      restGate // shared REST budget — 429 silences books+history alike
	connMu    sync.Mutex
	conn      *websocket.Conn
	subs      *quoteSubs
}

// NewTwelveData builds the provider. It does not connect until Start.
func NewTwelveData(cfg TwelveDataConfig) *TwelveData {
	if cfg.RESTBaseURL == "" {
		cfg.RESTBaseURL = "https://api.twelvedata.com"
	}
	if cfg.WSURL == "" {
		cfg.WSURL = "wss://ws.twelvedata.com/v1/quotes/price"
	}
	if cfg.Symbol == "" {
		cfg.Symbol = "XAU/USD"
	}
	if cfg.AssetID == "" {
		cfg.AssetID = "xauusd"
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = 30 * time.Second
	}
	if cfg.Transport == "" {
		cfg.Transport = "rest"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.CandleRefresh <= 0 {
		cfg.CandleRefresh = cacheRefreshAfter
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	t := &TwelveData{
		cfg:  cfg,
		log:  cfg.Logger,
		http: cfg.HTTPClient,
		dial: func(ctx context.Context, u string) (*websocket.Conn, *http.Response, error) {
			return websocket.Dial(ctx, u, nil)
		},
		now:   time.Now,
		state: StateDisabled,
		books: make(map[string]*candleBook),
		done:  make(chan struct{}),
		subs:  &quoteSubs{},
	}
	if t.http == nil {
		t.http = &http.Client{Timeout: 15 * time.Second}
	}
	for tf, def := range supportedTimeframes {
		t.books[tf] = &candleBook{def: def}
	}
	return t
}

// Start launches the connection supervisor. With no API key it parks in
// config_error — the historical app is unaffected.
func (t *TwelveData) Start(ctx context.Context) {
	t.once.Do(func() {
		cctx, cancel := context.WithCancel(ctx)
		t.cancel = cancel
		if t.cfg.APIKey == "" {
			t.setState(StateConfigError, "TWELVE_DATA_API_KEY is not set - live data unavailable")
			close(t.done)
			return
		}
		t.setState(StateConnecting, "")
		t.mu.Lock()
		t.started = true
		t.mu.Unlock()
		if t.cfg.Transport == "websocket" {
			go t.wsSupervise(cctx)
		} else {
			go t.pollSupervise(cctx)
		}
	})
}

// Close stops supervision and closes the upstream connection.
func (t *TwelveData) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.connMu.Lock()
	if t.conn != nil {
		_ = t.conn.Close(websocket.StatusNormalClosure, "shutdown")
		t.conn = nil
	}
	t.connMu.Unlock()
	t.mu.Lock()
	started := t.started
	t.mu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-t.done:
	case <-time.After(3 * time.Second):
	}
	return nil
}

// Status snapshots the provider. "stale" is computed from quote age so a
// quiet-but-connected feed reports freshness truthfully.
func (t *TwelveData) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := Status{
		Enabled:               true,
		State:                 t.state,
		Provider:              "twelvedata",
		Transport:             t.cfg.Transport,
		Symbol:                t.cfg.Symbol,
		LastError:             t.lastErr,
		StaleAfter:            t.cfg.StaleAfter,
		PollInterval:          t.cfg.PollInterval,
		EstimatedDailyCredits: t.estimatedDailyCredits(),
	}
	if t.quote != nil {
		st.LastQuoteAt = t.quote.ProviderTime
	}
	if !t.lastFetch.IsZero() {
		f := t.lastFetch
		st.LastFetchAt = &f
	}
	if t.state == StateConnected && t.quote != nil &&
		t.now().Sub(t.quote.ReceivedAt) > t.cfg.StaleAfter {
		st.State = StateStale
	}
	return st
}

// estimatedDailyCredits projects upstream API calls/day: one /quote per poll
// (REST transport only) plus one /time_series per timeframe per refresh —
// assuming every timeframe's book is viewed. Retries and failures are not
// counted; the real figure is at most this plus failure backoffs.
func (t *TwelveData) estimatedDailyCredits() int {
	const day = int(24 * time.Hour)
	quotes := 0
	if t.cfg.Transport == "rest" && t.cfg.PollInterval > 0 {
		quotes = day / int(t.cfg.PollInterval)
	}
	candles := 0
	if t.cfg.CandleRefresh > 0 {
		candles = len(t.books) * day / int(t.cfg.CandleRefresh)
	}
	return quotes + candles
}

// SubscribeQuotes registers fn for every accepted quote; see Provider.
func (t *TwelveData) SubscribeQuotes(fn func(Quote)) func() {
	return t.subs.add(fn)
}

// LatestQuote returns the newest validated quote.
func (t *TwelveData) LatestQuote() (Quote, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.quote == nil {
		return Quote{}, false
	}
	return *t.quote, true
}

// Candles returns cached history + the live-forming bar for tf, reconciling
// from REST when the cache is absent or stale. Fetches are bounded so a busy
// UI cannot hammer the provider.
func (t *TwelveData) Candles(ctx context.Context, tf string, limit int) ([]Candle, error) {
	book, ok := t.books[tf]
	if !ok {
		return nil, fmt.Errorf("unsupported timeframe %q", tf)
	}
	if limit <= 0 || limit > candleBookSize {
		limit = candleBookSize
	}
	t.maybeReconcile(book, tf)
	out := book.snapshot(limit)
	if len(out) == 0 {
		// Consistent with LatestQuote: no data is unavailable, not success —
		// a 200 with an empty list would mislead the UI into "waiting for
		// data" when the provider cannot deliver any.
		return nil, fmt.Errorf("no live candles available (state=%s)", t.Status().State)
	}
	return out, nil
}

// maybeReconcile refetches a book when it has never been fetched or its cache
// aged out, throttled to one in-flight fetch and minFetchGap between tries.
// It is a no-op when the provider cannot fetch (disabled, bad config).
func (t *TwelveData) maybeReconcile(book *candleBook, tf string) {
	if t.cfg.APIKey == "" || t.Status().State == StateConfigError {
		return
	}
	book.mu.Lock()
	stale := book.fetched.IsZero() || t.now().Sub(book.fetched) > t.cfg.CandleRefresh
	canTry := stale && !book.fetching && t.now().Sub(book.lastTry) > minFetchGap && !t.rest.suppressed(t.now())
	if canTry {
		book.fetching = true
		book.lastTry = t.now()
	}
	book.mu.Unlock()
	if !canTry {
		return
	}
	go func() {
		defer func() {
			book.mu.Lock()
			book.fetching = false
			book.mu.Unlock()
		}()
		// Detached from the HTTP request: a client disconnect must not abort
		// a cache refill that later requests rely on.
		fctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cs, err := t.fetchTimeSeries(fctx, tf, supportedTimeframes[tf].seedRows())
		if err != nil {
			t.log.Warn("live candle reconcile failed", "timeframe", tf, "err", err)
			t.setErr(fmt.Sprintf("candle fetch %s: %v", tf, err))
			return
		}
		book.replace(cs, t.now())
	}()
}

// wsSupervise owns the WS lifecycle: connect → subscribe → read; on failure
// it reschedules with bounded exponential backoff + jitter. Auth-style
// failures park the provider in config_error — retrying won't fix a bad key.
func (t *TwelveData) wsSupervise(ctx context.Context) {
	defer close(t.done)
	delay := initReconnectDelay
	dialFails := 0
	for {
		if ctx.Err() != nil {
			return
		}
		err := t.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if isAuthFailure(err) {
			t.setState(StateConfigError, fmt.Sprintf("provider rejected credentials or plan access: %v", err))
			return
		}
		if isDialFailure(err) {
			dialFails++
			// A connection that is repeatedly refused (e.g. a key without
			// WebSocket access — Twelve Data answers with a non-101 page, not
			// a handshake) will never heal on its own; report it honestly.
			if dialFails >= 5 {
				t.setState(StateConfigError,
					"connection repeatedly refused by provider — check TWELVE_DATA_API_KEY and plan WebSocket access")
				return
			}
		} else {
			dialFails = 0
		}
		t.setState(StateDisconnected, fmt.Sprintf("connection lost: %v", err))
		jitter := time.Duration(rand.Int63n(int64(delay) / 4))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay + jitter):
		}
		delay = min(delay*2, maxReconnectDelay)
	}
}

// session is one connect/subscribe/read cycle, returning the failure that
// ended it.
func (t *TwelveData) session(ctx context.Context) error {
	u, err := url.Parse(t.cfg.WSURL)
	if err != nil {
		return fmt.Errorf("ws url: %w", err)
	}
	q := u.Query()
	q.Set("apikey", t.cfg.APIKey)
	u.RawQuery = q.Encode()

	t.setState(StateConnecting, "")
	conn, resp, err := t.dial(ctx, u.String())
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return &authError{err: err}
		}
		return &dialError{err: err}
	}
	t.connMu.Lock()
	t.conn = conn
	t.connMu.Unlock()
	defer func() {
		t.connMu.Lock()
		t.conn = nil
		t.connMu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}()

	sub := wsSubscribe{Action: "subscribe"}
	sub.Params.Symbols = t.cfg.Symbol
	if err := conn.Write(ctx, websocket.MessageText, mustJSON(sub)); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	readErr := make(chan error, 1)
	go func() { readErr <- t.readLoop(conn) }()

	hb := time.NewTicker(heartbeatInterval)
	defer hb.Stop()
	for {
		select {
		case err := <-readErr:
			return err
		case <-hb.C:
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"action":"heartbeat"}`)); err != nil {
				return fmt.Errorf("heartbeat: %w", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// readLoop consumes provider events until the stream fails.
func (t *TwelveData) readLoop(conn *websocket.Conn) error {
	for {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			return err
		}
		var probe struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			continue // malformed frame — ignore, keep streaming
		}
		switch probe.Event {
		case "subscribe-status":
			var st wsSubscribeStatus
			if err := json.Unmarshal(data, &st); err == nil {
				t.onSubscribeStatus(st)
			}
		case "price":
			var p wsPrice
			if err := json.Unmarshal(data, &p); err == nil {
				t.onPrice(p)
			}
		}
	}
}

func (t *TwelveData) onSubscribeStatus(st wsSubscribeStatus) {
	for _, f := range st.Fails {
		if f.Symbol == t.cfg.Symbol {
			t.setErr(fmt.Sprintf("subscription refused for %s (symbol or plan not permitted)", f.Symbol))
			return
		}
	}
	for _, s := range st.Success {
		if s.Symbol == t.cfg.Symbol {
			t.setState(StateConnected, "")
			// (Re)fill candle books so the chart recovers any gap from a drop.
			for tf, book := range t.books {
				t.maybeReconcile(book, tf)
			}
			return
		}
	}
}

// onPrice validates and applies one tick. Rejects: wrong symbol, non-positive
// or non-finite price, zero/bad timestamp, and out-of-order updates.
func (t *TwelveData) onPrice(p wsPrice) {
	if p.Symbol != t.cfg.Symbol {
		return
	}
	if math.IsNaN(p.Price) || math.IsInf(p.Price, 0) || p.Price <= 0 {
		t.setErr(fmt.Sprintf("rejected invalid price %v", p.Price))
		return
	}
	if p.Timestamp <= 0 {
		t.setErr("rejected price event with missing timestamp")
		return
	}
	ts := time.Unix(p.Timestamp, 0).UTC()
	// Reject absurd provider timestamps (clock-skew guard: ±24h).
	if d := t.now().Sub(ts); d > 24*time.Hour || d < -24*time.Hour {
		t.setErr(fmt.Sprintf("rejected price event with implausible timestamp %s", ts))
		return
	}

	t.applyQuote(Quote{
		AssetID:      t.cfg.AssetID,
		Symbol:       p.Symbol,
		Price:        p.Price,
		PriceKind:    "last",
		Bid:          p.Bid,
		Ask:          p.Ask,
		DayVolume:    p.DayVolume,
		ProviderTime: &ts,
		ReceivedAt:   t.now().UTC(),
	})
}

// applyQuote commits a validated quote: it drops out-of-order provider
// timestamps, refreshes the cache and lastFetch, marks the provider
// connected, and folds the price into every timeframe's forming candle.
// Book buckets use the provider timestamp; only when the source supplies
// none does our own receive time define the bucket.
func (t *TwelveData) applyQuote(q Quote) {
	t.mu.Lock()
	if q.ProviderTime != nil && !t.lastTick.IsZero() && q.ProviderTime.Before(t.lastTick) {
		t.mu.Unlock()
		return // out-of-order update
	}
	if q.ProviderTime != nil {
		t.lastTick = *q.ProviderTime
	}
	t.quote = &q
	t.lastFetch = q.ReceivedAt
	if t.state != StateConnected {
		t.state = StateConnected
	}
	t.mu.Unlock()

	bucket := q.ReceivedAt
	if q.ProviderTime != nil {
		bucket = *q.ProviderTime
	}
	for _, book := range t.books {
		book.onTick(bucket, q.Price)
	}
	t.subs.notify(q)
}

// fetchTimeSeries pulls recent candles from REST /time_series.
// Values arrive newest-first; the result is ascending.
func (t *TwelveData) fetchTimeSeries(ctx context.Context, tf string, limit int) ([]Candle, error) {
	def, ok := supportedTimeframes[tf]
	if !ok {
		return nil, fmt.Errorf("unsupported timeframe %q", tf)
	}
	u, err := url.Parse(t.cfg.RESTBaseURL + "/time_series")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("symbol", t.cfg.Symbol)
	q.Set("interval", def.tdInterval)
	q.Set("outputsize", strconv.Itoa(limit))
	q.Set("timezone", "UTC")
	q.Set("apikey", t.cfg.APIKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if err := t.rest.check(t.now()); err != nil {
		return nil, err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		// *url.Error embeds the full request URL — including the apikey query
		// param. Re-wrap so credentials can never reach logs or API responses.
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("provider request failed: %w", ue.Err)
		}
		return nil, errors.New("provider request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		t.rest.trip(t.now(), parseRetryAfter(resp.Header.Get("Retry-After")))
		return nil, &RateLimitError{
			Err:        errors.New("provider rate limit exceeded (429) — retry later"),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}

	var ts tdTimeSeries
	if err := json.Unmarshal(body, &ts); err != nil {
		return nil, fmt.Errorf("malformed provider response: %w", err)
	}
	if ts.Status == "error" || ts.Code != 0 {
		return nil, tdError(ts.Code, ts.Message)
	}

	out := make([]Candle, 0, len(ts.Values))
	for i := len(ts.Values) - 1; i >= 0; i-- { // newest-first → ascending
		v := ts.Values[i]
		c, err := parseTDCandle(v.Datetime, v.Open, v.High, v.Low, v.Close, v.Volume)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	// Sessionize every fetched row into the canonical Kyiv civil labels —
	// 1:1 relabel for intraday intervals, real aggregation for 4h/1d books
	// that fetched 1h seeds.
	out = aggregateSession(out, def)
	return out, nil
}

func (t *TwelveData) HistoryFloor() time.Time {
	// Twelve Data intraday history is plan-dependent; the REST floor is
	// enforced server-side, so zero means "as far back as the plan allows".
	return time.Time{}
}

// FetchHistoryPages pages /time_series over [from, to), delivering
// session-labeled candles with the raw covered span per page. The endpoint
// returns the NEWEST outputsize rows of the requested window, so the driver
// walks backward — each reported span extends coverage downward from `to`.
func (t *TwelveData) FetchHistoryPages(ctx context.Context, tf string, from, to time.Time, onPage PageFunc) error {
	def, ok := supportedTimeframes[tf]
	if !ok {
		return fmt.Errorf("unsupported timeframe %q", tf)
	}
	const pageSize = 5000
	sz := &pageSessionizer{def: def}
	cur := to
	first := true
	for cur.After(from) {
		if !first {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		first = false
		page, err := t.fetchTimeSeriesWindow(ctx, def, from, cur, pageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return onPage(sz.emit(nil, true), cur, cur)
		}
		// page is ascending, newest ≤ cur. Covered span [page[0].Time, cur).
		coveredFrom := page[0].Time
		// Backward direction: hold the earliest pending bucket — it may gain
		// rows from the next (older) page. Reuse pageSessionizer by pushing
		// the page then emitting only labels AFTER its earliest bucket.
		emitted := sz.emitPrefix(page, len(page) >= pageSize)
		if err := onPage(emitted, coveredFrom, cur); err != nil {
			return err
		}
		if len(page) < pageSize {
			return onPage(sz.emit(nil, true), coveredFrom, coveredFrom)
		}
		next := page[0].Time
		if !next.Before(cur) {
			return onPage(sz.emit(nil, true), next, next)
		}
		cur = next
	}
	return onPage(sz.emit(nil, true), cur, cur)
}

// FetchHistory is FetchHistoryPages collecting all pages.
func (t *TwelveData) FetchHistory(ctx context.Context, tf string, from, to time.Time) ([]Candle, error) {
	var out []Candle
	err := t.FetchHistoryPages(ctx, tf, from, to, func(page []Candle, _, _ time.Time) error {
		out = append(out, page...)
		return nil
	})
	return out, err
}

// fetchTimeSeriesWindow pulls one page: the newest `limit` bars in [from, to).
func (t *TwelveData) fetchTimeSeriesWindow(ctx context.Context, def intervalDef, from, to time.Time, limit int) ([]Candle, error) {
	u, err := url.Parse(t.cfg.RESTBaseURL + "/time_series")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("symbol", t.cfg.Symbol)
	q.Set("interval", def.tdInterval)
	q.Set("outputsize", strconv.Itoa(limit))
	q.Set("timezone", "UTC")
	q.Set("start_date", from.Format("2006-01-02 15:04:05"))
	q.Set("end_date", to.Format("2006-01-02 15:04:05"))
	q.Set("apikey", t.cfg.APIKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if err := t.rest.check(t.now()); err != nil {
		return nil, err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("provider request failed: %w", ue.Err)
		}
		return nil, errors.New("provider request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		t.rest.trip(t.now(), parseRetryAfter(resp.Header.Get("Retry-After")))
		return nil, &RateLimitError{
			Err:        errors.New("provider rate limit exceeded (429) — retry later"),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	var ts tdTimeSeries
	if err := json.Unmarshal(body, &ts); err != nil {
		return nil, fmt.Errorf("malformed provider response: %w", err)
	}
	if ts.Status == "error" || ts.Code != 0 {
		return nil, tdError(ts.Code, ts.Message)
	}
	out := make([]Candle, 0, len(ts.Values))
	for i := len(ts.Values) - 1; i >= 0; i-- {
		v := ts.Values[i]
		c, err := parseTDCandle(v.Datetime, v.Open, v.High, v.Low, v.Close, v.Volume)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// parseTDCandle converts one provider row. Datetime accepts "2006-01-02" or
// "2006-01-02 15:04:05" (UTC, as requested via timezone=UTC). Volume stays nil
// when absent — commodities may not report it.
func parseTDCandle(dt, o, h, l, c, vol string) (Candle, error) {
	var ts time.Time
	var err error
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		ts, err = time.ParseInLocation(layout, dt, time.UTC)
		if err == nil {
			break
		}
	}
	if err != nil {
		return Candle{}, fmt.Errorf("bad candle datetime %q", dt)
	}
	cv := Candle{Time: ts.UTC()}
	for _, dst := range []struct {
		p *float64
		s string
	}{
		{&cv.Open, o}, {&cv.High, h}, {&cv.Low, l}, {&cv.Close, c},
	} {
		f, err := strconv.ParseFloat(dst.s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
			return Candle{}, fmt.Errorf("bad candle price %q", dst.s)
		}
		*dst.p = f
	}
	if cv.High < cv.Low {
		return Candle{}, fmt.Errorf("candle high < low at %s", dt)
	}
	if vol != "" {
		if f, err := strconv.ParseFloat(vol, 64); err == nil && !math.IsNaN(f) && f >= 0 {
			cv.Volume = &f
		}
	}
	return cv, nil
}

// --- REST polling -----------------------------------------------------------

// tdQuote is the Twelve Data /quote response. Most numeric fields arrive as
// strings; timestamp/last_quote_at are unix seconds. 1 API credit per call.
// For XAU/USD (a commodity) several fields may be absent — we keep what the
// provider actually sends and leave the rest nil.
type tdQuote struct {
	Symbol       string `json:"symbol"`
	Datetime     string `json:"datetime"`
	Timestamp    *int64 `json:"timestamp"`
	LastQuoteAt  *int64 `json:"last_quote_at"`
	Close        string `json:"close"`
	Volume       string `json:"volume"`
	IsMarketOpen *bool  `json:"is_market_open"`
	Status       string `json:"status"`
	Code         int    `json:"code"`
	Message      string `json:"message"`
}

// pollSupervise is the REST transport: one managed worker polling /quote on a
// fixed cadence — never per-browser-request. Failures back off exponentially
// (bounded) with jitter; auth-style rejections park in config_error.
func (t *TwelveData) pollSupervise(ctx context.Context) {
	defer close(t.done)
	interval := t.cfg.PollInterval
	delay := interval
	fails := 0
	for {
		if ctx.Err() != nil {
			return
		}
		err := t.pollOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		switch {
		case err == nil:
			fails = 0
			delay = interval
			// Seed/reconcile candle books — the books' own throttle (≥30s
			// between fetches, ≥60s cache TTL) bounds the upstream cost.
			for tf, book := range t.books {
				t.maybeReconcile(book, tf)
			}
		case isAuthFailure(err):
			t.setState(StateConfigError,
				fmt.Sprintf("provider rejected credentials or plan access: %v", err))
			return
		default:
			fails++
			t.setState(StateDisconnected, fmt.Sprintf("poll failed: %v", err))
			// Back off 2^fails × interval, capped — a rate limit or outage
			// must not be hammered.
			backoff := interval << min(fails, 4)
			if backoff > maxReconnectDelay {
				backoff = maxReconnectDelay
			}
			delay = backoff + time.Duration(rand.Int63n(int64(backoff)/4))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// pollOnce performs one quote fetch and applies the result.
func (t *TwelveData) pollOnce(ctx context.Context) error {
	q, err := t.fetchQuote(ctx)
	if err != nil {
		return err
	}
	t.applyQuote(q)
	return nil
}

// fetchQuote calls GET /quote for the configured symbol and maps the
// provider fields onto a Quote. Missing provider timestamps stay nil —
// the status surface reports that limitation instead of inventing one.
func (t *TwelveData) fetchQuote(ctx context.Context) (Quote, error) {
	u, err := url.Parse(t.cfg.RESTBaseURL + "/quote")
	if err != nil {
		return Quote{}, err
	}
	q := u.Query()
	q.Set("symbol", t.cfg.Symbol)
	q.Set("apikey", t.cfg.APIKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Quote{}, err
	}
	if err := t.rest.check(t.now()); err != nil {
		return Quote{}, err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return Quote{}, fmt.Errorf("provider request failed: %w", ue.Err)
		}
		return Quote{}, errors.New("provider request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Quote{}, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Quote{}, &authError{err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		t.rest.trip(t.now(), parseRetryAfter(resp.Header.Get("Retry-After")))
		return Quote{}, &RateLimitError{
			Err:        errors.New("provider rate limit exceeded (429) — retry later"),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}

	var tq tdQuote
	if err := json.Unmarshal(body, &tq); err != nil {
		return Quote{}, fmt.Errorf("malformed provider response: %w", err)
	}
	if tq.Status == "error" || tq.Code != 0 {
		return Quote{}, tdError(tq.Code, tq.Message)
	}
	if tq.Symbol != t.cfg.Symbol {
		return Quote{}, fmt.Errorf("provider returned symbol %q, expected %q", tq.Symbol, t.cfg.Symbol)
	}
	price, err := strconv.ParseFloat(tq.Close, 64)
	if err != nil || math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
		return Quote{}, fmt.Errorf("invalid provider close price %q", tq.Close)
	}

	quote := Quote{
		AssetID:    t.cfg.AssetID,
		Symbol:     tq.Symbol,
		Price:      price,
		PriceKind:  "last",
		ReceivedAt: t.now().UTC(),
	}
	// Provider timestamp: prefer last_quote_at (actual quote time), then the
	// row timestamp. None supplied → stays nil, honestly.
	src := tq.LastQuoteAt
	if src == nil {
		src = tq.Timestamp
	}
	if src != nil {
		ts := time.Unix(*src, 0).UTC()
		if d := t.now().Sub(ts); d > 24*time.Hour || d < -24*time.Hour {
			return Quote{}, fmt.Errorf("implausible provider timestamp %s", ts)
		}
		quote.ProviderTime = &ts
	}
	if tq.Volume != "" {
		if f, err := strconv.ParseFloat(tq.Volume, 64); err == nil && !math.IsNaN(f) && f >= 0 {
			quote.DayVolume = &f
		}
	}
	return quote, nil
}

// tdError classifies a provider error body: auth/plan rejections are
// authError (no retry will help), 429 is a distinct rate-limit message.
func tdError(code int, msg string) error {
	if msg == "" {
		msg = "provider error"
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return &authError{err: errors.New(msg)}
	}
	if code == http.StatusTooManyRequests {
		return errors.New("provider rate limit exceeded (429) — retry later")
	}
	return fmt.Errorf("provider error %d: %s", code, msg)
}

// --- helpers ----------------------------------------------------------------

type authError struct{ err error }

func (e *authError) Error() string { return e.err.Error() }

// dialError marks failures before the session starts — refused handshakes,
// DNS, TLS, timeouts. Distinguished from mid-stream drops so that repeated
// refusals can escalate to config_error.
type dialError struct{ err error }

func (e *dialError) Error() string { return "dial: " + e.err.Error() }

func isAuthFailure(err error) bool {
	var ae *authError
	return errors.As(err, &ae)
}

func isDialFailure(err error) bool {
	var de *dialError
	return errors.As(err, &de)
}

// redact scrubs the API key out of any string that could reach logs or API
// responses — the credential must never surface anywhere.
func (t *TwelveData) redact(s string) string {
	if k := t.cfg.APIKey; k != "" {
		return strings.ReplaceAll(s, k, "[redacted]")
	}
	return s
}

// SubscribeState mirrors Tiingo: transition-time delivery for coverage
// tracking. Optional capability — providers implement it themselves.
func (t *TwelveData) SubscribeState(fn func(prev, next State, detail string)) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	i := len(t.stateSubs)
	t.stateSubs = append(t.stateSubs, fn)
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.stateSubs[i] = nil
	}
}

func (t *TwelveData) setState(s State, errMsg string) {
	t.mu.Lock()
	prev := t.state
	t.state = s
	if errMsg != "" || s != StateDisconnected {
		t.lastErr = t.redact(errMsg)
	}
	if prev != s && len(t.stateSubs) > 0 {
		subs := append([]func(State, State, string){}, t.stateSubs...)
		d := t.lastErr
		t.mu.Unlock()
		for _, fn := range subs {
			if fn != nil {
				fn(prev, s, d)
			}
		}
		return
	}
	t.mu.Unlock()
}

func (t *TwelveData) setErr(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastErr = t.redact(msg)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
