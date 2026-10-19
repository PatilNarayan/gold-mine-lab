// Package live defines the live-market-data seam: a single managed upstream
// provider connection feeding a thread-safe quote cache and bounded candle
// books. Live data is observational only — it never feeds the backtest
// engine, strategies, or persisted research records.
package live

import (
	"context"
	"time"
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
	Price        float64    // last traded/streamed price
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
	// Close stops workers and releases the upstream connection.
	Close() error
}
