// Package repository defines the persistence contracts consumed by the
// service layer, plus the row types they exchange. Implementations live in
// repository/postgres. Repositories contain database access only — no
// business logic.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// ErrNotFound is returned when a lookup misses; services map it to 404.
var ErrNotFound = errors.New("repository: not found")

// RunRow mirrors the backtest_runs table.
type RunRow struct {
	ID             string
	StrategyID     string
	AssetID        string
	Timeframe      string
	StartDate      string // YYYY-MM-DD
	EndDate        string
	InitialCapital float64
	PositionSize   float64
	Commission     float64
	Slippage       float64
	Parameters     map[string]any
	Status         string
	EquityCurve    []EquityPointRow // nil until completed
	Error          *string
	CreatedAt      time.Time
	CompletedAt    *time.Time
}

// EquityPointRow is one point of a persisted equity curve.
type EquityPointRow struct {
	Time     time.Time `json:"time"`
	Equity   float64   `json:"equity"`
	Drawdown float64   `json:"drawdown"`
}

// TradeRow mirrors the backtest_trades table.
type TradeRow struct {
	ID         string
	RunID      string
	StrategyID string
	AssetID    string
	Side       string
	EntryTime  time.Time
	EntryPrice float64
	ExitTime   *time.Time
	ExitPrice  *float64
	Quantity   float64
	Pnl        *float64
	Status     string
	Result     string
}

// TradeQuery carries filters + pagination for the trades table.
type TradeQuery struct {
	StrategyID string
	Side       string
	Result     string
	From       time.Time // zero = unbounded
	To         time.Time // zero = unbounded; exclusive
	Page       int
	PageSize   int
}

// MetricValue is one (name, value) pair for a run.
type MetricValue struct {
	Name  string
	Value float64
}

type AssetRepository interface {
	UpsertAsset(ctx context.Context, a market.Asset) error
	ListAssets(ctx context.Context) ([]market.Asset, error)
	GetAsset(ctx context.Context, id string) (market.Asset, error)
}

type StrategyRepository interface {
	UpsertStrategy(ctx context.Context, s strategy.Definition) error
	ListStrategies(ctx context.Context) ([]strategy.Definition, error)
	GetStrategy(ctx context.Context, id string) (strategy.Definition, error)
}

type SignalRepository interface {
	// InsertSignals is idempotent per (strategy_id, asset_id, timestamp).
	InsertSignals(ctx context.Context, signals []strategy.Signal) (int, error)
	ListSignals(ctx context.Context, assetID, strategyID string, limit int) ([]strategy.Signal, error)
	LatestSignal(ctx context.Context, strategyID string) (*strategy.Signal, error)
}

type RunRepository interface {
	// CreateRun inserts a queued run and returns its generated ID.
	CreateRun(ctx context.Context, r RunRow) (string, error)
	// MarkRunRunning transitions queued → running.
	MarkRunRunning(ctx context.Context, runID string) error
	// CompleteRun persists trades, metrics and equity curve, then marks the
	// run completed — all inside one transaction.
	CompleteRun(ctx context.Context, runID string, trades []TradeRow, metrics []MetricValue, equity []EquityPointRow) error
	// FailRun marks the run failed with a safe message.
	FailRun(ctx context.Context, runID, message string) error
	GetRun(ctx context.Context, runID string) (RunRow, error)
	ListRuns(ctx context.Context, strategyID string) ([]RunRow, error)
	LatestCompletedRunID(ctx context.Context) (string, error)
}

type TradeRepository interface {
	ListTradesByRun(ctx context.Context, runID string) ([]TradeRow, error)
	// ListAllClosed returns every closed trade across runs, exit-time ascending
	// (used to build the aggregate equity curve).
	ListAllClosed(ctx context.Context) ([]TradeRow, error)
	// ListTradesFiltered applies filters + pagination and returns the page
	// plus the total matching row count.
	ListTradesFiltered(ctx context.Context, q TradeQuery) (rows []TradeRow, total int, err error)
}

type MetricRepository interface {
	GetMetrics(ctx context.Context, runID string) (map[string]float64, error)
	// AggregateMetrics computes sums/means across completed runs.
	AggregateMetrics(ctx context.Context) (netPnl, winRateMean, maxDrawdown float64, totalRuns int, err error)
	// BestStrategyByNetPnl returns the strategy of the completed run with the
	// highest net P&L.
	BestStrategyByNetPnl(ctx context.Context) (id, name string, ok bool, err error)
}
