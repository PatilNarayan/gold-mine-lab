package api

import (
	"context"
	"errors"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// This file defines the wire contract shared with the frontend: DTO types use
// the exact camelCase JSON field names the client expects, and service
// interfaces are the seam handlers depend on. Phase 1 implements them with an
// in-memory demo service; later phases swap in Postgres-backed repositories.

// ErrNotFound is returned by services for unknown resources → HTTP 404.
var ErrNotFound = errors.New("not found")

// ValidationError is returned by services for bad input → HTTP 400.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// SeriesPoint is a timestamped numeric point on a chart overlay.
type SeriesPoint struct {
	Time  string  `json:"time"` // ISO-8601 UTC
	Value float64 `json:"value"`
}

// CandleSeries is a candle payload with precomputed overlays.
type CandleSeries struct {
	AssetID   string           `json:"assetId"`
	Timeframe market.Timeframe `json:"timeframe"`
	Candles   []market.Candle  `json:"candles"`
	Overlays  Overlays         `json:"overlays"`
}

// Overlays holds precomputed indicator series (EMA20/EMA50).
type Overlays struct {
	EMA20 []SeriesPoint `json:"ema20"`
	EMA50 []SeriesPoint `json:"ema50"`
}

// MarketSummary is the market overview tile payload.
type MarketSummary struct {
	Asset         market.Asset     `json:"asset"`
	Timeframe     market.Timeframe `json:"timeframe"`
	LastClose     float64          `json:"lastClose"`
	Change        float64          `json:"change"`
	ChangePercent float64          `json:"changePercent"`
	AsOf          string           `json:"asOf"`
	Status        string           `json:"status"` // "Open" | "Closed" | "Historical / Demo"
}

// StrategyPerformance is precomputed per-strategy aggregate stats.
type StrategyPerformance struct {
	NetPnl         float64 `json:"netPnl"`
	WinRate        float64 `json:"winRate"`
	TotalBacktests int     `json:"totalBacktests"`
}

// StrategyListItem = strategy + last signal + performance for the list view.
type StrategyListItem struct {
	strategy.Definition
	LastSignal  *strategy.Signal    `json:"lastSignal"`
	Performance StrategyPerformance `json:"performance"`
}

// BacktestMetrics mirrors the planned backtest_metrics table.
type BacktestMetrics struct {
	NetPnl       float64 `json:"netPnl"`
	ReturnPct    float64 `json:"returnPct"`
	WinRate      float64 `json:"winRate"`
	ProfitFactor float64 `json:"profitFactor"`
	MaxDrawdown  float64 `json:"maxDrawdown"`
	TotalTrades  int     `json:"totalTrades"`
	AvgTradePnl  float64 `json:"avgTradePnl"`
}

// BacktestRun mirrors the planned backtest_runs table.
type BacktestRun struct {
	ID             string           `json:"id"`
	StrategyID     string           `json:"strategyId"`
	AssetID        string           `json:"assetId"`
	Timeframe      market.Timeframe `json:"timeframe"`
	StartDate      string           `json:"startDate"`
	EndDate        string           `json:"endDate"`
	InitialCapital float64          `json:"initialCapital"`
	PositionSize   float64          `json:"positionSize"`
	Commission     float64          `json:"commission"`
	Slippage       float64          `json:"slippage"`
	Parameters     map[string]any   `json:"parameters"`
	Status         string           `json:"status"` // queued|running|completed|failed
	CreatedAt      string           `json:"createdAt"`
	CompletedAt    *string          `json:"completedAt"`
	Error          *string          `json:"error"`
}

// BacktestTrade mirrors the planned backtest_trades table.
type BacktestTrade struct {
	ID         string   `json:"id"`
	RunID      string   `json:"runId"`
	StrategyID string   `json:"strategyId"`
	AssetID    string   `json:"assetId"`
	Side       string   `json:"side"` // BUY|SELL
	EntryTime  string   `json:"entryTime"`
	EntryPrice float64  `json:"entryPrice"`
	ExitTime   *string  `json:"exitTime"`
	ExitPrice  *float64 `json:"exitPrice"`
	Quantity   float64  `json:"quantity"`
	Pnl        *float64 `json:"pnl"`
	Status     string   `json:"status"` // open|closed
	Result     string   `json:"result"` // win|loss|open
}

// EquityPoint is one point on an equity curve (drawdown as fraction ≤ 0).
type EquityPoint struct {
	Time     string  `json:"time"`
	Equity   float64 `json:"equity"`
	Drawdown float64 `json:"drawdown"`
}

// BacktestDetail is the full payload for GET /api/backtests/:id.
type BacktestDetail struct {
	Run      BacktestRun          `json:"run"`
	Strategy *strategy.Definition `json:"strategy"`
	Metrics  *BacktestMetrics     `json:"metrics"`
	Equity   []EquityPoint        `json:"equity"`
	Trades   []BacktestTrade      `json:"trades"`
}

// DashboardSummary is the aggregate payload for GET /api/backtests/summary.
type DashboardSummary struct {
	TotalBacktests   int           `json:"totalBacktests"`
	BestStrategyID   *string       `json:"bestStrategyId"`
	BestStrategyName *string       `json:"bestStrategyName"`
	WinRate          float64       `json:"winRate"`
	NetPnl           float64       `json:"netPnl"`
	MaxDrawdown      float64       `json:"maxDrawdown"`
	Equity           []EquityPoint `json:"equity"`
}

// BacktestRequest is the body of POST /api/backtests.
type BacktestRequest struct {
	AssetID        string           `json:"assetId"`
	StrategyID     string           `json:"strategyId"`
	Timeframe      market.Timeframe `json:"timeframe"`
	StartDate      string           `json:"startDate"`
	EndDate        string           `json:"endDate"`
	InitialCapital float64          `json:"initialCapital"`
	PositionSize   float64          `json:"positionSize"`
	Commission     float64          `json:"commission"`
	Slippage       float64          `json:"slippage"`
	Parameters     map[string]any   `json:"parameters"`
}

// TradeFilters mirrors the frontend's query-param filter set.
type TradeFilters struct {
	StrategyID string
	Side       string // BUY|SELL
	Result     string // win|loss|open
	From       string // ISO date
	To         string // ISO date
	Page       int
	PageSize   int
}

// PagedTrades is a page of trades plus total count.
type PagedTrades struct {
	Items    []BacktestTrade `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
}

// MarketService supplies assets, candle series, and market overviews.
type MarketService interface {
	Assets(ctx context.Context) ([]market.Asset, error)
	Candles(ctx context.Context, assetID string, tf market.Timeframe) (CandleSeries, error)
	MarketSummary(ctx context.Context, assetID string, tf market.Timeframe) (MarketSummary, error)
}

// StrategyService lists strategies and their signals.
type StrategyService interface {
	List(ctx context.Context) ([]StrategyListItem, error)
	Get(ctx context.Context, id string) (strategy.Definition, error)
	Signals(ctx context.Context, assetID, strategyID string, limit int) ([]strategy.Signal, error)
}

// BacktestService manages backtest runs and their results.
type BacktestService interface {
	ListRuns(ctx context.Context, strategyID string) ([]BacktestRun, error)
	GetRun(ctx context.Context, runID string) (BacktestDetail, error)
	LatestRunID(ctx context.Context) (string, error) // "" when no completed run exists
	DashboardSummary(ctx context.Context) (DashboardSummary, error)
	Execute(ctx context.Context, req BacktestRequest) (BacktestRun, error)
}

// TradeService returns filtered, paginated trades.
type TradeService interface {
	ListTrades(ctx context.Context, f TradeFilters) (PagedTrades, error)
}
