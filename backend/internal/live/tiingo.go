// Package live — Tiingo provider implementation (optional alternative to
// Twelve Data, selected via LIVE_MARKET_PROVIDER=tiingo).
//
// One managed WebSocket subscription to the Tiingo Forex firehose streams
// XAUUSD top-of-book updates into the shared quote cache and per-timeframe
// forming candles. REST /tiingo/fx/{ticker}/prices supplies recent candles on
// connect and on bounded reconciles. The API token travels only inside the
// subscribe message body or an Authorization header — never in a URL, so it
// cannot leak through request logs or error strings.
//
// Verified against https://www.tiingo.com/documentation/websockets/forex
// and https://www.tiingo.com/documentation/forex:
//   - WS: wss://api.tiingo.com/fx (token NOT in the URL)
//   - subscribe: {"eventName":"subscribe","authorization":"TOKEN",
//     "eventData":{"thresholdLevel":5,"tickers":["xauusd"]}}
//   - frames: {"service":"fx","messageType":"A","data":[
//     "Q","xauusd","<ISO ts>",bidSize,bidPrice,midPrice,askSize,askPrice]}
//     messageType I = info/subscription ack, E = error, H = heartbeat (~30s)
//   - midPrice is a top-of-book midpoint, NOT an executed trade price — the
//     adapter tags it PriceKind "mid" so nothing downstream mislabels it.
//   - REST: GET /tiingo/fx/{ticker}/prices?startDate=YYYY-MM-DD&resampleFreq=
//     15min with header "Authorization: Token TOKEN" — rows ascending.
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// TiingoConfig configures the managed Tiingo provider.
type TiingoConfig struct {
	APIToken       string        // TIINGO_API_TOKEN — required when enabled
	WSURL          string        // default wss://api.tiingo.com/fx
	RESTBaseURL    string        // default https://api.tiingo.com
	Ticker         string        // provider ticker — default "xauusd"
	Symbol         string        // display symbol reported back — default "XAU/USD"
	AssetID        string        // canonical asset id reported back — "xauusd"
	StaleAfter     time.Duration // quote age after which state reports "stale"
	CandleRefresh  time.Duration // per-timeframe REST reconcile TTL — default 5m
	ThresholdLevel int           // FX firehose granularity — default 5 (all top-of-book)
	HTTPClient     *http.Client
	Logger         *slog.Logger
}

const tiingoIdleTimeout = 90 * time.Second // 3× the documented 30s heartbeat

// --- ws wire messages -------------------------------------------------------

// tiingoSubscribe is the documented subscription frame. The token rides in
// the message body's authorization field — the WebSocket URL stays clean.
type tiingoSubscribe struct {
	EventName     string `json:"eventName"`
	Authorization string `json:"authorization"`
	EventData     struct {
		ThresholdLevel int      `json:"thresholdLevel"`
		Tickers        []string `json:"tickers"`
	} `json:"eventData"`
}

// tiingoFrame is the server frame envelope. Data is an object for I/E frames
// (e.g. {"subscriptionId":N}) and a heterogeneous array for A frames, so it
// stays raw until the message type is known.
type tiingoFrame struct {
	Service     string          `json:"service"`
	MessageType string          `json:"messageType"`
	Data        json.RawMessage `json:"data"`
	Response    struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"response"`
}

// --- REST wire messages -----------------------------------------------------

// tiingoPriceRow is one row of GET /tiingo/fx/{ticker}/prices.
type tiingoPriceRow struct {
	Date   string   `json:"date"`
	Ticker string   `json:"ticker"`
	Open   float64  `json:"open"`
	High   float64  `json:"high"`
	Low    float64  `json:"low"`
	Close  float64  `json:"close"`
	Volume *float64 `json:"volume"` // absent for many fx series — stays nil
}

// tiingoErrorBody is the REST error envelope ({"detail":"..."} or a plain
// string body).
type tiingoErrorBody struct {
	Detail string `json:"detail"`
}

// Tiingo is a managed Tiingo provider: one FX-websocket subscription feeding
// quote + candle caches, with bounded REST reconciles for history.
type Tiingo struct {
	cfg  TiingoConfig
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
	lastFetch time.Time // last accepted quote or successful candle fetch
	lastTick  time.Time // newest accepted provider timestamp
	books     map[string]*candleBook
	rest      restGate // shared REST budget — 429 silences books+history alike
	connMu    sync.Mutex
	conn      *websocket.Conn
	lastFrame atomic.Int64 // unix nanos of the most recent inbound frame
	subs      *quoteSubs
}

// NewTiingo builds the provider. It does not connect until Start.
func NewTiingo(cfg TiingoConfig) *Tiingo {
	if cfg.WSURL == "" {
		cfg.WSURL = "wss://api.tiingo.com/fx"
	}
	if cfg.RESTBaseURL == "" {
		cfg.RESTBaseURL = "https://api.tiingo.com"
	}
	if cfg.Ticker == "" {
		cfg.Ticker = "xauusd"
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
	if cfg.CandleRefresh <= 0 {
		cfg.CandleRefresh = cacheRefreshAfter
	}
	if cfg.ThresholdLevel == 0 {
		cfg.ThresholdLevel = 5
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	p := &Tiingo{
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
	if p.http == nil {
		p.http = &http.Client{Timeout: 15 * time.Second}
	}
	for tf, def := range supportedTimeframes {
		p.books[tf] = &candleBook{def: def}
	}
	return p
}

// Start launches the connection supervisor. With no token it parks in
// config_error — the historical app is unaffected.
func (p *Tiingo) Start(ctx context.Context) {
	p.once.Do(func() {
		cctx, cancel := context.WithCancel(ctx)
		p.cancel = cancel
		if p.cfg.APIToken == "" {
			p.setState(StateConfigError, "TIINGO_API_TOKEN is not set - live data unavailable")
			close(p.done)
			return
		}
		p.setState(StateConnecting, "")
		p.mu.Lock()
		p.started = true
		p.mu.Unlock()
		go p.wsSupervise(cctx)
	})
}

// Close stops supervision and closes the upstream connection.
func (p *Tiingo) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	p.connMu.Lock()
	if p.conn != nil {
		_ = p.conn.Close(websocket.StatusNormalClosure, "shutdown")
		p.conn = nil
	}
	p.connMu.Unlock()
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
	}
	return nil
}

// Status snapshots the provider. "stale" is computed from quote age so a
// quiet-but-connected feed reports freshness truthfully.
func (p *Tiingo) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{
		Enabled:               true,
		State:                 p.state,
		Provider:              "tiingo",
		Transport:             "websocket",
		Symbol:                p.cfg.Symbol,
		LastError:             p.lastErr,
		StaleAfter:            p.cfg.StaleAfter,
		EstimatedDailyCredits: p.estimatedDailyCredits(),
	}
	if p.quote != nil {
		st.LastQuoteAt = p.quote.ProviderTime
	}
	if !p.lastFetch.IsZero() {
		f := p.lastFetch
		st.LastFetchAt = &f
	}
	if p.state == StateConnected && p.quote != nil &&
		p.now().Sub(p.quote.ReceivedAt) > p.cfg.StaleAfter {
		st.State = StateStale
	}
	return st
}

// estimatedDailyCredits projects upstream REST calls/day. The quote stream is
// a WebSocket subscription (no per-tick requests), so only candle reconciles
// count: one /tiingo/fx/{t}/prices per timeframe per refresh.
func (p *Tiingo) estimatedDailyCredits() int {
	if p.cfg.CandleRefresh <= 0 {
		return 0
	}
	return len(p.books) * int(24*time.Hour) / int(p.cfg.CandleRefresh)
}

// SubscribeQuotes registers fn for every accepted quote; see Provider.
func (p *Tiingo) SubscribeQuotes(fn func(Quote)) func() {
	return p.subs.add(fn)
}

// LatestQuote returns the newest validated quote.
func (p *Tiingo) LatestQuote() (Quote, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.quote == nil {
		return Quote{}, false
	}
	return *p.quote, true
}

// Candles returns cached history + the live-forming bar for tf, reconciling
// from REST when the cache is absent or stale.
func (p *Tiingo) Candles(ctx context.Context, tf string, limit int) ([]Candle, error) {
	book, ok := p.books[tf]
	if !ok {
		return nil, fmt.Errorf("unsupported timeframe %q", tf)
	}
	if limit <= 0 || limit > candleBookSize {
		limit = candleBookSize
	}
	p.maybeReconcile(book, tf)
	out := book.snapshot(limit)
	if len(out) == 0 {
		return nil, fmt.Errorf("no live candles available (state=%s)", p.Status().State)
	}
	return out, nil
}

// maybeReconcile mirrors TwelveData's: refetch a book when never fetched or
// aged out, throttled to one in-flight fetch and minFetchGap between tries.
func (p *Tiingo) maybeReconcile(book *candleBook, tf string) {
	if p.cfg.APIToken == "" || p.Status().State == StateConfigError {
		return
	}
	book.mu.Lock()
	stale := book.fetched.IsZero() || p.now().Sub(book.fetched) > p.cfg.CandleRefresh
	canTry := stale && !book.fetching && p.now().Sub(book.lastTry) > minFetchGap && !p.rest.suppressed(p.now())
	if canTry {
		book.fetching = true
		book.lastTry = p.now()
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
		fctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cs, err := p.fetchPrices(fctx, tf, supportedTimeframes[tf].seedRows())
		if err != nil {
			p.log.Warn("tiingo candle reconcile failed", "timeframe", tf, "err", err)
			p.setErr(fmt.Sprintf("candle fetch %s: %v", tf, err))
			return
		}
		book.replace(cs, p.now())
		p.mu.Lock()
		p.lastFetch = p.now()
		p.mu.Unlock()
	}()
}

// wsSupervise owns the WS lifecycle with bounded backoff + jitter. Auth-style
// failures and repeated dial refusals park the provider in config_error —
// retrying will not heal a bad token or missing entitlement.
func (p *Tiingo) wsSupervise(ctx context.Context) {
	defer close(p.done)
	delay := initReconnectDelay
	dialFails := 0
	for {
		if ctx.Err() != nil {
			return
		}
		err := p.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if isAuthFailure(err) {
			p.setState(StateConfigError, fmt.Sprintf("provider rejected credentials or plan access: %v", err))
			return
		}
		if isDialFailure(err) {
			dialFails++
			if dialFails >= 5 {
				p.setState(StateConfigError,
					"connection repeatedly refused by provider — check TIINGO_API_TOKEN and Forex firehose access")
				return
			}
		} else {
			dialFails = 0
		}
		p.setState(StateDisconnected, fmt.Sprintf("connection lost: %v", err))
		jitter := time.Duration(rand.Int63n(int64(delay) / 4))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay + jitter):
		}
		delay = min(delay*2, maxReconnectDelay)
	}
}

// session is one connect/subscribe/read cycle. The connection URL carries no
// token; authentication happens inside the subscribe frame body.
func (p *Tiingo) session(ctx context.Context) error {
	p.setState(StateConnecting, "")
	conn, resp, err := p.dial(ctx, p.cfg.WSURL)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return &authError{err: err}
		}
		return &dialError{err: err}
	}
	p.connMu.Lock()
	p.conn = conn
	p.connMu.Unlock()
	defer func() {
		p.connMu.Lock()
		p.conn = nil
		p.connMu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}()

	sub := tiingoSubscribe{EventName: "subscribe", Authorization: p.cfg.APIToken}
	sub.EventData.ThresholdLevel = p.cfg.ThresholdLevel
	sub.EventData.Tickers = []string{p.cfg.Ticker}
	if err := conn.Write(ctx, websocket.MessageText, mustJSON(sub)); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	p.lastFrame.Store(time.Now().UnixNano())
	readErr := make(chan error, 1)
	go func() { readErr <- p.readLoop(conn) }()

	// The server sends a heartbeat every ~30s. No frame for 3× that means the
	// connection is silently dead — close it so the supervisor reconnects.
	watch := time.NewTicker(15 * time.Second)
	defer watch.Stop()
	for {
		select {
		case err := <-readErr:
			return err
		case <-watch.C:
			if time.Since(time.Unix(0, p.lastFrame.Load())) > tiingoIdleTimeout {
				return errors.New("no frames within heartbeat window — connection assumed dead")
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// readLoop consumes provider frames until the stream fails. Frames are
// handled synchronously — no unbounded queue can grow behind a slow consumer.
func (p *Tiingo) readLoop(conn *websocket.Conn) error {
	for {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			return err
		}
		p.lastFrame.Store(time.Now().UnixNano())
		var f tiingoFrame
		if err := json.Unmarshal(data, &f); err != nil {
			continue // malformed frame — ignore, keep streaming
		}
		switch f.MessageType {
		case "A":
			p.onUpdate(f.Data)
		case "I":
			p.onInfo(f)
		case "E":
			return p.classifyError(f)
		case "H":
			// heartbeat — lastFrame already bumped
		}
	}
}

// onInfo handles subscription acknowledgements. A 200 response means the
// subscription was accepted — report connected and refill candle books.
func (p *Tiingo) onInfo(f tiingoFrame) {
	switch {
	case f.Response.Code == http.StatusOK:
		p.setState(StateConnected, "")
		for tf, book := range p.books {
			p.maybeReconcile(book, tf)
		}
	case f.Response.Code == http.StatusUnauthorized || f.Response.Code == http.StatusForbidden:
		p.setErr(fmt.Sprintf("subscription rejected (code %d)", f.Response.Code))
	default:
		p.log.Info("tiingo info frame", "code", f.Response.Code, "msg", f.Response.Message)
	}
}

// classifyError turns an "E" frame into a terminal session error: auth codes
// park in config_error, everything else triggers a bounded reconnect.
func (p *Tiingo) classifyError(f tiingoFrame) error {
	msg := f.Response.Message
	if msg == "" {
		msg = "provider error"
	}
	switch f.Response.Code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &authError{err: fmt.Errorf("HTTP %d: %s", f.Response.Code, msg)}
	case http.StatusTooManyRequests:
		return fmt.Errorf("provider rate limit exceeded (429) — retry later")
	default:
		return fmt.Errorf("provider error %d: %s", f.Response.Code, msg)
	}
}

// onUpdate parses one top-of-book "A" frame:
// data = ["Q", ticker, ISO-ts, bidSize, bidPrice, midPrice, askSize, askPrice].
// Only the configured ticker is accepted; malformed rows are dropped, never
// applied.
func (p *Tiingo) onUpdate(raw json.RawMessage) {
	var d []json.RawMessage
	if err := json.Unmarshal(raw, &d); err != nil || len(d) != 8 {
		return
	}
	var kind string
	if json.Unmarshal(d[0], &kind) != nil || kind != "Q" {
		return // only top-of-book quote updates are consumed
	}
	var ticker string
	if json.Unmarshal(d[1], &ticker) != nil ||
		!strings.EqualFold(ticker, p.cfg.Ticker) {
		return // unrequested symbol — ignore
	}
	var tsStr string
	if json.Unmarshal(d[2], &tsStr) != nil {
		return
	}
	ts, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		p.setErr(fmt.Sprintf("rejected quote with bad timestamp %q", tsStr))
		return
	}
	ts = ts.UTC()
	if skew := p.now().Sub(ts); skew > 24*time.Hour || skew < -24*time.Hour {
		p.setErr(fmt.Sprintf("rejected quote with implausible timestamp %s", ts))
		return
	}
	num := func(i int) *float64 {
		var f float64
		if json.Unmarshal(d[i], &f) != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return &f
	}
	bidSize, bid, mid, askSize, ask := num(3), num(4), num(5), num(6), num(7)
	_ = bidSize
	_ = askSize // sizes are parsed for validation but not surfaced

	// midPrice is the documented midpoint — never a last trade. When absent,
	// fall back to (bid+ask)/2 only when both sides are supplied and sane.
	price := 0.0
	if mid != nil && *mid > 0 {
		price = *mid
	} else if bid != nil && ask != nil && *bid > 0 && *ask > 0 && *bid <= *ask {
		price = (*bid + *ask) / 2
	}
	if price <= 0 {
		p.setErr("rejected quote with no usable mid/bid/ask price")
		return
	}
	if bid != nil && ask != nil && *bid > *ask {
		p.setErr(fmt.Sprintf("rejected quote with crossed book bid=%v ask=%v", *bid, *ask))
		return
	}
	positive := bid != nil && *bid > 0 && ask != nil && *ask > 0
	if !positive {
		bid, ask = nil, nil // partial book — don't surface a single side
	}

	p.applyQuote(Quote{
		AssetID:      p.cfg.AssetID,
		Symbol:       p.cfg.Symbol,
		Price:        price,
		PriceKind:    "mid",
		Bid:          bid,
		Ask:          ask,
		ProviderTime: &ts,
		ReceivedAt:   p.now().UTC(),
	})
}

// applyQuote commits a validated quote: drops out-of-order provider
// timestamps, refreshes the cache, marks connected, and folds the mid price
// into every timeframe's forming candle. Event-driven: no ticks, no updates.
func (p *Tiingo) applyQuote(q Quote) {
	p.mu.Lock()
	if q.ProviderTime != nil && !p.lastTick.IsZero() && q.ProviderTime.Before(p.lastTick) {
		p.mu.Unlock()
		return // out-of-order update
	}
	if q.ProviderTime != nil {
		p.lastTick = *q.ProviderTime
	}
	p.quote = &q
	p.lastFetch = q.ReceivedAt
	if p.state != StateConnected {
		p.state = StateConnected
	}
	p.mu.Unlock()

	bucket := q.ReceivedAt
	if q.ProviderTime != nil {
		bucket = *q.ProviderTime
	}
	for _, book := range p.books {
		book.onTick(bucket, q.Price)
	}
	p.subs.notify(q)
}

// fetchPrices pulls recent candles from REST /tiingo/fx/{ticker}/prices with
// the token in the Authorization header — never in the URL. Rows arrive
// ascending.
func (p *Tiingo) fetchPrices(ctx context.Context, tf string, limit int) ([]Candle, error) {
	def, ok := supportedTimeframes[tf]
	if !ok {
		return nil, fmt.Errorf("unsupported timeframe %q", tf)
	}
	u, err := url.Parse(p.cfg.RESTBaseURL + "/tiingo/fx/" + p.cfg.Ticker + "/prices")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	// Fetch a window covering `limit` SEED-interval rows plus buffer for
	// closures — session books seed from 1h rows, not their own bar width.
	start := p.now().Add(-def.seedBar() * time.Duration(limit) * 2)
	q.Set("startDate", start.Format("2006-01-02"))
	q.Set("resampleFreq", def.tiFreq)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+p.cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")
	if err := p.rest.check(p.now()); err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("provider request failed: %w", ue.Err)
		}
		return nil, errors.New("provider request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &authError{err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, tiingoErrDetail(body))}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		p.rest.trip(p.now(), parseRetryAfter(resp.Header.Get("Retry-After")))
		return nil, &RateLimitError{
			Err:        fmt.Errorf("provider rate limit exceeded (429): %s", tiingoErrDetail(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, tiingoErrDetail(body))
	}

	var rows []tiingoPriceRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("malformed provider response: %w", err)
	}
	out := make([]Candle, 0, len(rows))
	for _, r := range rows {
		c, err := parseTiingoCandle(r)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	// Sessionize every fetched row into the canonical Kyiv civil labels —
	// 1:1 relabel for intraday intervals, real aggregation for 4h/1d books
	// that fetched 1h seeds — then trim to the requested window.
	out = aggregateSession(out, def)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// tiingoHistoryFloor is the provider's documented earliest FX date — the API
// rejects earlier startDate values.
var tiingoHistoryFloor = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

func (p *Tiingo) HistoryFloor() time.Time { return tiingoHistoryFloor }

// pageSessionizer emits canonical session-labeled candles progressively.
// Intraday timeframes relabel 1:1 so each row emits independently; 4h/1d
// aggregate 1h rows and a session bucket is only complete once a later
// bucket begins — the tail bucket stays pending until flush.
type pageSessionizer struct {
	def     intervalDef
	pending []Candle
}

// emit returns safely-emittable labeled candles. flush=true emits the final
// pending bucket.
func (s *pageSessionizer) emit(page []Candle, flush bool) []Candle {
	if !s.def.aggregate {
		return aggregateSession(page, s.def)
	}
	s.pending = append(s.pending, page...)
	ag := aggregateSession(s.pending, s.def)
	if len(ag) <= 1 {
		if flush {
			s.pending = nil
			return ag
		}
		return nil
	}
	if flush {
		s.pending = nil
		return ag
	}
	// Hold back the last label's raw rows — its bucket isn't proven complete.
	last := ag[len(ag)-1].Time
	kept := s.pending[:0]
	for _, c := range s.pending {
		if !sessionBucket(c.Time, s.def).Before(last) {
			kept = append(kept, c)
		}
	}
	s.pending = kept
	return ag[:len(ag)-1]
}

// emitPrefix is the backward-paging counterpart: page contains rows OLDER
// than pending, so the earliest aggregated bucket may gain rows from a later
// page. moreComing=false flushes everything.
func (s *pageSessionizer) emitPrefix(page []Candle, moreComing bool) []Candle {
	if !s.def.aggregate {
		return aggregateSession(page, s.def)
	}
	s.pending = append(page, s.pending...)
	ag := aggregateSession(s.pending, s.def)
	if len(ag) == 0 {
		return nil
	}
	if !moreComing {
		s.pending = nil
		return ag
	}
	first := ag[0].Time
	kept := s.pending[:0]
	for _, c := range s.pending {
		if sessionBucket(c.Time, s.def).Equal(first) {
			kept = append(kept, c)
		}
	}
	s.pending = kept
	return ag[1:]
}

// FetchHistoryPages pages the REST prices endpoint over [from, to),
// delivering session-labeled candles plus the raw covered span per page so
// the caller can commit resumable coverage. Tiingo's startDate is
// day-granular and each response is capped (~5–7k rows forward from start);
// a page can only truncate on a day boundary because no supported interval
// produces more bars per day than the cap, so advancing by whole days is
// lossless. Empty spans (weekends/closed market) report coverage without
// bars — syncing a span and producing rows are different statements.
func (p *Tiingo) FetchHistoryPages(ctx context.Context, tf string, from, to time.Time, onPage PageFunc) error {
	def, ok := supportedTimeframes[tf]
	if !ok {
		return fmt.Errorf("unsupported timeframe %q", tf)
	}
	sz := &pageSessionizer{def: def}
	cur := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	end := to.UTC()
	first := true
	for cur.Before(end) {
		// Politeness: Tiingo rate-limits bursts — pace page requests.
		if !first {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		first = false
		page, err := p.fetchPricesPage(ctx, def, cur, end)
		if err != nil {
			return err
		}
		next := cur.AddDate(0, 0, 1)
		if len(page) > 0 {
			last := page[len(page)-1].Time
			if n := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1); n.After(next) {
				next = n
			}
		}
		if err := onPage(sz.emit(page, false), cur, next); err != nil {
			return err
		}
		cur = next
	}
	return onPage(sz.emit(nil, true), end, end)
}

// FetchHistory pages the REST prices endpoint over [from, to) and returns
// ascending canonical session-labeled candles.
func (p *Tiingo) FetchHistory(ctx context.Context, tf string, from, to time.Time) ([]Candle, error) {
	var out []Candle
	err := p.FetchHistoryPages(ctx, tf, from, to, func(page []Candle, _, _ time.Time) error {
		out = append(out, page...)
		return nil
	})
	return out, err
}

// fetchPricesPage issues one REST request for [from, to) and returns the raw
// (UTC instant) rows, ascending.
func (p *Tiingo) fetchPricesPage(ctx context.Context, def intervalDef, from, to time.Time) ([]Candle, error) {
	u, err := url.Parse(p.cfg.RESTBaseURL + "/tiingo/fx/" + p.cfg.Ticker + "/prices")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("startDate", from.Format("2006-01-02"))
	if !to.IsZero() {
		q.Set("endDate", to.Format("2006-01-02"))
	}
	q.Set("resampleFreq", def.tiFreq)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+p.cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")
	if err := p.rest.check(p.now()); err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
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
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &authError{err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, tiingoErrDetail(body))}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		p.rest.trip(p.now(), parseRetryAfter(resp.Header.Get("Retry-After")))
		return nil, &RateLimitError{
			Err:        fmt.Errorf("provider rate limit exceeded (429): %s", tiingoErrDetail(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, tiingoErrDetail(body))
	}
	var rows []tiingoPriceRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("malformed provider response: %w", err)
	}
	out := make([]Candle, 0, len(rows))
	for _, r := range rows {
		c, err := parseTiingoCandle(r)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// tiingoErrDetail extracts {"detail":"..."} from an error body if present.
// The body can never contain the token — it travels in a header only.
func tiingoErrDetail(body []byte) string {
	var eb tiingoErrorBody
	if json.Unmarshal(body, &eb) == nil && eb.Detail != "" {
		return eb.Detail
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// parseTiingoCandle converts one REST row. Date arrives as "2019-06-30T00:00:00"
// or with a UTC offset — parsed as UTC when naive, matching the documented
// exchange-timezone-free FX convention.
func parseTiingoCandle(r tiingoPriceRow) (Candle, error) {
	ts, err := time.Parse(time.RFC3339Nano, r.Date)
	if err != nil {
		if ts, err = time.ParseInLocation("2006-01-02T15:04:05", r.Date, time.UTC); err != nil {
			return Candle{}, fmt.Errorf("bad candle date %q", r.Date)
		}
	}
	cv := Candle{Time: ts.UTC()}
	for _, v := range []float64{r.Open, r.High, r.Low, r.Close} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return Candle{}, fmt.Errorf("bad candle price in row at %s", r.Date)
		}
	}
	cv.Open, cv.High, cv.Low, cv.Close = r.Open, r.High, r.Low, r.Close
	if cv.High < cv.Low {
		return Candle{}, fmt.Errorf("candle high < low at %s", r.Date)
	}
	if r.Volume != nil && *r.Volume >= 0 && !math.IsNaN(*r.Volume) {
		cv.Volume = r.Volume
	}
	return cv, nil
}

// redact scrubs the token out of any string that could reach logs or API
// responses — the credential must never surface anywhere.
func (p *Tiingo) redact(s string) string {
	if k := p.cfg.APIToken; k != "" {
		return strings.ReplaceAll(s, k, "[redacted]")
	}
	return s
}

// SubscribeState registers fn for connection-state transitions — delivered
// at transition time, not at the next Status() sample. Used by the quote
// recorder's coverage tracker; the returned func unsubscribes.
func (p *Tiingo) SubscribeState(fn func(prev, next State, detail string)) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := len(p.stateSubs)
	p.stateSubs = append(p.stateSubs, fn)
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.stateSubs[i] = nil
	}
}

func (p *Tiingo) setState(s State, errMsg string) {
	p.mu.Lock()
	prev := p.state
	p.state = s
	if errMsg != "" || s != StateDisconnected {
		p.lastErr = p.redact(errMsg)
	}
	if prev != s && len(p.stateSubs) > 0 {
		subs := append([]func(State, State, string){}, p.stateSubs...)
		d := p.lastErr
		p.mu.Unlock()
		for _, fn := range subs {
			if fn != nil {
				fn(prev, s, d)
			}
		}
		return
	}
	p.mu.Unlock()
}

func (p *Tiingo) setErr(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = p.redact(msg)
}
