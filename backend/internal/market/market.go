// Package market describes market data primitives: tradeable assets,
// timeframes, OHLCV candles, and the DataProvider seam that historical and
// live sources will implement.
//
// Planned implementations (not built in Phase 1):
//   - HistoricalCSVProvider — loads candles from CSV exports for research runs
//   - LiveMarketDataProvider — streams live quotes (never broker/execution)
package market

import (
	"context"
	"time"
)

// CanonicalAssetID is the single instrument this deployment serves.
// The platform is XAUUSD-only by product decision: catalog, selectors, and
// all endpoints (historical and live) reject anything else with a clear
// error rather than silently substituting another symbol.
const CanonicalAssetID = "xauusd"

// Asset is a tradeable instrument. Asset-agnostic by design — symbols like
// XAUUSD, XAGUSD, MSFT or TSLA are data, never literals in code.
// JSON tags match the API contract the frontend expects.
type Asset struct {
	ID            string `json:"id"`
	Symbol        string `json:"symbol"`
	Name          string `json:"name"`
	AssetClass    string `json:"assetClass"`    // e.g. "metal", "equity", "forex"
	QuoteCurrency string `json:"quoteCurrency"` // e.g. "USD"
}

// Timeframe is a candle interval, e.g. "1m", "5m", "15m", "1h", "4h", "1d".
type Timeframe string

const (
	Timeframe1m  Timeframe = "1m"
	Timeframe5m  Timeframe = "5m"
	Timeframe15m Timeframe = "15m"
	Timeframe1h  Timeframe = "1h"
	Timeframe4h  Timeframe = "4h"
	Timeframe1d  Timeframe = "1d"
)

// Candle is one OHLCV bar. Time is stored as a UTC instant and marshals as
// RFC3339 — the wire format the frontend parses.
type Candle struct {
	Time   time.Time `json:"time"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume float64   `json:"volume"`
}

// DataProvider supplies historical candles for an asset/timeframe window.
// Callers pass context for cancellation; implementations must return candles
// in ascending time order.
type DataProvider interface {
	GetCandles(ctx context.Context, asset Asset, tf Timeframe, from, to time.Time) ([]Candle, error)
}
