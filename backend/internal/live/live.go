// Package live defines the live-market-data seam: a single managed upstream
// provider connection feeding a thread-safe quote cache and bounded candle
// books. Live data is observational only — it never feeds the backtest
// engine, strategies, or persisted research records.
package live

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata" // embedded tz DB so Europe/Kyiv loads on every OS
)

// State is the provider's connection/data state reported to the API.
type State string

const (
	// StateDisabled means LIVE_MARKET_ENABLED is off — endpoints report it
	// honestly instead of fabricating data.
	StateDisabled State = "disabled"
	// StateConfigError means the feature is enabled but misconfigured
	// (e.g. missing/invalid API key or a provider rejection).
	StateConfigError State = "config_error"
	// StateConnecting means a connection attempt is in flight or the
	// provider subscription has not been confirmed yet.
	StateConnecting State = "connecting"
	// StateConnected means the stream is subscribed and receiving updates.
	StateConnected State = "connected"
	// StateStale means the connection is up but no quote has arrived within
	// the configured staleness window (market closed or silent feed).
	StateStale State = "stale"
	// StateDisconnected means the stream dropped and reconnection is
	// scheduled with backoff.
	StateDisconnected State = "disconnected"
)

// Quote is the latest provider price update. Bid/Ask/Volume stay nil when the
// provider does not supply them — they are never fabricated. ProviderTime is
// nil when the source supplies no meaningful timestamp; consumers must not
// substitute the receive time for it.
type Quote struct {
	AssetID      string     // canonical asset id, e.g. "xauusd"
	Symbol       string     // provider symbol, e.g. "XAU/USD"
	Price        float64    // streamed price — semantics per PriceKind
	PriceKind    string     // "last" = last trade/close; "mid" = top-of-book midpoint (never labeled a trade)
	Bid          *float64   // provider bid when supplied
	Ask          *float64   // provider ask when supplied
	DayVolume    *float64   // provider day_volume when supplied
	ProviderTime *time.Time // provider event timestamp (UTC) when supplied
	ReceivedAt   time.Time  // local receipt timestamp (UTC)
}

// Status is a point-in-time snapshot of the managed provider.
type Status struct {
	Enabled      bool
	State        State
	Provider     string     // e.g. "twelvedata"
	Transport    string     // "websocket" | "rest" — the actual transport in use
	Symbol       string     // provider symbol actually subscribed
	LastQuoteAt  *time.Time // provider timestamp of newest quote
	LastFetchAt  *time.Time // last successful upstream fetch (poll or tick)
	LastError    string     // operator-facing; never contains credentials
	StaleAfter   time.Duration
	PollInterval time.Duration // REST poll cadence; 0 for WebSocket
	// EstimatedDailyCredits is the projected upstream API calls/day at the
	// current intervals: 86400/poll quotes + books×86400/refresh candles.
	EstimatedDailyCredits int
}

// Candle is one OHLC bar from the live provider. Volume is nil when the
// provider does not supply it for the instrument — never synthesized.
type Candle struct {
	Time   time.Time `json:"time"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume *float64  `json:"volume"`
}

// RateLimitError signals the provider refused the request for quota reasons
// (HTTP 429). RetryAfter carries the provider's Retry-After header when
// supplied; callers must not hammer the endpoint before that elapses.
type RateLimitError struct {
	Err        error
	RetryAfter time.Duration // 0 when the provider gave no guidance
}

func (e *RateLimitError) Error() string { return e.Err.Error() }
func (e *RateLimitError) Unwrap() error { return e.Err }

// RateLimitRetryAfter satisfies market.RetryableRateLimit — the persisted
// retry gate honors the provider's guidance.
func (e *RateLimitError) RateLimitRetryAfter() time.Duration { return e.RetryAfter }

// restGate suppresses all REST calls while the provider's quota is
// exhausted — candle-book fetches and history pages share one upstream
// budget, so one exhausted endpoint must silence all of them.
type restGate struct{ until atomic.Int64 } // unix-nanos; zero = open

func (g *restGate) suppressed(now time.Time) bool { return now.UnixNano() < g.until.Load() }

// check returns a synthetic RateLimitError while gated — callers treat it
// like a real 429 (persisted backoff) without touching the network.
func (g *restGate) check(now time.Time) error {
	if u := g.until.Load(); now.UnixNano() < u {
		return &RateLimitError{
			Err:        errors.New("provider rate limit — gated locally"),
			RetryAfter: time.Until(time.Unix(0, u)),
		}
	}
	return nil
}

// trip closes the gate for at least `hint` (floor 5m — a hard 429 means the
// budget is exhausted; retrying in seconds burns whatever remains).
func (g *restGate) trip(now time.Time, hint time.Duration) {
	if hint < 5*time.Minute {
		hint = 5 * time.Minute
	}
	if u := now.Add(hint); u.UnixNano() > g.until.Load() {
		g.until.Store(u.UnixNano())
	}
}

// PageFunc receives each session-labeled candle page plus the raw source
// span it covered — the store commits coverage only for fully-fetched spans.
type PageFunc func(page []Candle, coveredFrom, coveredTo time.Time) error

// Provider is the managed upstream live-data connection. Implementations own
// their reconnect lifecycle; Start must return after spawning its workers.
type Provider interface {
	// Start begins connection management. It is idempotent and returns
	// immediately; use Status() to observe progress.
	Start(ctx context.Context)
	// Status reports the current connection/data snapshot.
	Status() Status
	// LatestQuote returns the newest accepted quote, if any.
	LatestQuote() (Quote, bool)
	// Candles returns up to limit ascending candles for tf, merging cached
	// provider history with the forming bar built from streamed quotes.
	Candles(ctx context.Context, tf string, limit int) ([]Candle, error)
	// SubscribeQuotes registers fn for every accepted provider quote. Events
	// are delivered synchronously in arrival order; the returned func
	// unsubscribes. Used by the API's browser stream — never per-request.
	SubscribeQuotes(fn func(Quote)) func()
	// FetchHistory pages the provider's historical candles over [from, to),
	// ascending, in the canonical session-label convention. Used to backfill
	// the persistent historical store — not for live chart responses.
	FetchHistory(ctx context.Context, tf string, from, to time.Time) ([]Candle, error)
	// FetchHistoryPages is FetchHistory with per-page delivery: each page is
	// sessionized (complete session buckets only) and reported with the raw
	// span it covered so the caller can persist resumable progress.
	FetchHistoryPages(ctx context.Context, tf string, from, to time.Time, onPage PageFunc) error
	// HistoryFloor is the earliest instant the provider can supply candles
	// for; zero when unknown.
	HistoryFloor() time.Time
	// Close stops workers and releases the upstream connection.
	Close() error
}

// quoteSubs is the shared listener registry used by both providers. Listeners
// are invoked synchronously after the quote is committed — they must be cheap
// and must not call back into the provider.
type quoteSubs struct {
	mu    sync.Mutex
	next  int
	funcs map[int]func(Quote)
}

func (s *quoteSubs) add(fn func(Quote)) func() {
	s.mu.Lock()
	if s.funcs == nil {
		s.funcs = map[int]func(Quote){}
	}
	id := s.next
	s.next++
	s.funcs[id] = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.funcs, id)
		s.mu.Unlock()
	}
}

func (s *quoteSubs) notify(q Quote) {
	s.mu.Lock()
	fns := make([]func(Quote), 0, len(s.funcs))
	for _, fn := range s.funcs {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(q)
	}
}

// brokerLoc is the market-session zone the historical dataset is labeled in —
// MT5 broker time (EET/EEST ≈ Europe/Kyiv). Session books bucket on its civil
// boundaries so live 4h/1d candles describe the same sessions the CSV
// provider reports.
var brokerLoc = func() *time.Location {
	l, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return time.UTC
	}
	return l
}()

// sessionBucket returns the candle label for instant ts under the platform's
// canonical contract: broker civil time (Europe/Kyiv) floored to the bar
// width, rendered as a naive UTC timestamp — identical to what the CSV
// provider produces for the same market interval. Labels are session
// identifiers, not absolute instants; during the autumn fall-back two real
// hours share one label, exactly as they do in the broker export.
func sessionBucket(ts time.Time, d intervalDef) time.Time {
	lt := ts.In(brokerLoc)
	h := lt.Hour()
	if d.bar >= 24*time.Hour {
		h = 0
	} else if d.bar >= time.Hour {
		h -= h % int(d.bar.Hours())
	}
	m := 0
	if d.bar < time.Hour {
		m = lt.Minute() - lt.Minute()%int(d.bar/time.Minute)
	}
	return time.Date(lt.Year(), lt.Month(), lt.Day(), h, m, 0, 0, time.UTC)
}

// aggregateSession folds ascending seed-interval rows into session-labeled
// candles — the same aggregation the datasync tool performs for the versioned
// snapshot, so live and historical session candles are built identically.
func aggregateSession(rows []Candle, d intervalDef) []Candle {
	out := make([]Candle, 0, len(rows)/4)
	for _, r := range rows {
		label := sessionBucket(r.Time, d)
		if n := len(out); n > 0 && out[n-1].Time.Equal(label) {
			c := &out[n-1]
			if r.High > c.High {
				c.High = r.High
			}
			if r.Low < c.Low {
				c.Low = r.Low
			}
			c.Close = r.Close
			if c.Volume != nil && r.Volume != nil {
				v := *c.Volume + *r.Volume
				c.Volume = &v
			}
			continue
		}
		out = append(out, Candle{Time: label, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close, Volume: r.Volume})
	}
	return out
}

// parseRetryAfter reads a Retry-After header — whole seconds, or an HTTP
// date — and returns the wait duration (0 when absent/unparseable).
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}
