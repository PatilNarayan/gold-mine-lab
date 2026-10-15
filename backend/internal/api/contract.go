package api

import (
	"context"
	"errors"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/internal/strategygraph"
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

// StrategyPerformance reports experiment count + the LATEST completed run's
// metrics. Independent runs differ in window/capital/params — summing P&L or
// averaging win rates across them would misrepresent the strategy.
// nil latestRun* = no completed run yet.
type StrategyPerformance struct {
	TotalBacktests     int     `json:"totalBacktests"`
	LatestRunID        *string `json:"latestRunId"`
	LatestRunTimeframe *string `json:"latestRunTimeframe"`
	LatestRunStartDate *string `json:"latestRunStartDate"`
	LatestRunEndDate   *string `json:"latestRunEndDate"`
	LatestRunNetPnl    float64 `json:"latestRunNetPnl"`
	LatestRunWinRate   float64 `json:"latestRunWinRate"`
}

// StrategyListItem = strategy + last signal + performance for the list view.
type StrategyListItem struct {
	strategy.Definition
	LastSignal  *strategy.Signal    `json:"lastSignal"`
	Performance StrategyPerformance `json:"performance"`
}

// BacktestMetrics mirrors the planned backtest_metrics table.
type BacktestMetrics struct {
	NetPnl        float64  `json:"netPnl"`
	ReturnPct     float64  `json:"returnPct"`
	WinRate       float64  `json:"winRate"`
	ProfitFactor  *float64 `json:"profitFactor"`
	MaxDrawdown   float64  `json:"maxDrawdown"`
	TotalTrades   int      `json:"totalTrades"`
	WinningTrades int      `json:"winningTrades"`
	LosingTrades  int      `json:"losingTrades"`
	AvgTradePnl   float64  `json:"avgTradePnl"`
	GrossProfit   float64  `json:"grossProfit"`
	GrossLoss     float64  `json:"grossLoss"`
	// TotalCommissions is nil on legacy runs that predate the metric.
	TotalCommissions *float64 `json:"totalCommissions"`
	// Risk metrics (Phase 6a): nil when undefined — no qualifying trades,
	// a zero denominator, or a legacy run that lacks the metric row.
	AvgWin               *float64 `json:"avgWin"`          // avg NET pnl of winners
	AvgLoss              *float64 `json:"avgLoss"`         // avg NET loss, positive magnitude
	PayoffRatio          *float64 `json:"payoffRatio"`     // avgWin / avgLoss
	Expectancy           *float64 `json:"expectancy"`      // net pnl per closed trade
	RecoveryFactor       *float64 `json:"recoveryFactor"`  // netPnl / max DD amount
	TimeInMarketPct      *float64 `json:"timeInMarketPct"` // % of eval bars in-market
	MaxConsecutiveLosses *int     `json:"maxConsecutiveLosses"`
	BarsInMarket         *int     `json:"barsInMarket"`
	EligibleBars         *int     `json:"eligibleBars"`
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
	// Provenance (migration 000002). nil on pre-2.1 rows: those recorded
	// gross P&L on trades and lack cost decomposition.
	EngineVersion   *string `json:"engineVersion"`
	StrategyVersion *string `json:"strategyVersion"`
	Dataset         *string `json:"dataset"`
	// Experiment membership: comparison (000003) or walk-forward (000004).
	ComparisonID  *string `json:"comparisonId,omitempty"`
	WalkForwardID *string `json:"walkForwardId,omitempty"`
	Sample        *string `json:"sample,omitempty"`
}

// BacktestTrade mirrors the planned backtest_trades table.
// Pnl is NET of commissions (engine ≥2.1); GrossPnl/commissions are null on
// legacy rows where pnl was stored gross.
type BacktestTrade struct {
	ID              string   `json:"id"`
	RunID           string   `json:"runId"`
	StrategyID      string   `json:"strategyId"`
	AssetID         string   `json:"assetId"`
	Side            string   `json:"side"` // BUY|SELL
	EntryTime       string   `json:"entryTime"`
	EntryPrice      float64  `json:"entryPrice"`
	ExitTime        *string  `json:"exitTime"`
	ExitPrice       *float64 `json:"exitPrice"`
	Quantity        float64  `json:"quantity"`
	GrossPnl        *float64 `json:"grossPnl"`
	Pnl             *float64 `json:"pnl"` // net of commissions
	EntryCommission *float64 `json:"entryCommission"`
	ExitCommission  *float64 `json:"exitCommission"`
	Status          string   `json:"status"` // open|closed
	Result          string   `json:"result"` // win|loss|open — classified on net
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
// Independent runs are NOT a portfolio: totalBacktests is an experiment
// count and the performance figures + equity curve describe the latest
// completed run only (identified by latestRunId).
type DashboardSummary struct {
	TotalBacktests        int           `json:"totalBacktests"`
	LatestRunID           *string       `json:"latestRunId"`
	LatestRunStrategyID   *string       `json:"latestRunStrategyId"`
	LatestRunStrategyName *string       `json:"latestRunStrategyName"`
	WinRate               float64       `json:"winRate"`
	NetPnl                float64       `json:"netPnl"`
	MaxDrawdown           float64       `json:"maxDrawdown"`
	Equity                []EquityPoint `json:"equity"` // latest run's curve
}

// ComparisonLeg is one strategy + its parameter overrides inside a comparison.
type ComparisonLeg struct {
	StrategyID string         `json:"strategyId"`
	Parameters map[string]any `json:"parameters"`
}

// ComparisonRequest is the body of POST /api/comparisons. Every leg shares
// the window, capital, position sizing, and cost assumptions — identical
// experiment conditions; only strategy parameters may differ.
// The optional OOS window must be strictly after the in-sample end date.
type ComparisonRequest struct {
	Legs                 []ComparisonLeg  `json:"legs"`
	AssetID              string           `json:"assetId"`
	Timeframe            market.Timeframe `json:"timeframe"`
	StartDate            string           `json:"startDate"` // in-sample window
	EndDate              string           `json:"endDate"`
	OutOfSampleStartDate *string          `json:"oosStartDate,omitempty"`
	OutOfSampleEndDate   *string          `json:"oosEndDate,omitempty"`
	InitialCapital       float64          `json:"initialCapital"`
	PositionSize         float64          `json:"positionSize"`
	Commission           float64          `json:"commission"`
	Slippage             float64          `json:"slippage"`
}

// ComparisonResult pairs a member run with its metrics and equity curve.
type ComparisonResult struct {
	Run     BacktestRun      `json:"run"`
	Sample  string           `json:"sample"` // "in_sample" | "out_of_sample"
	Metrics *BacktestMetrics `json:"metrics"`
	Equity  []EquityPoint    `json:"equity"`
}

// Comparison is a saved experiment group (summary in list, full detail on GET).
type Comparison struct {
	ID             string             `json:"id"`
	Status         string             `json:"status"` // pending|running|completed|partial_failed|failed
	AssetID        string             `json:"assetId"`
	Timeframe      string             `json:"timeframe"`
	StartDate      string             `json:"startDate"`
	EndDate        string             `json:"endDate"`
	OosStartDate   *string            `json:"oosStartDate"`
	OosEndDate     *string            `json:"oosEndDate"`
	InitialCapital float64            `json:"initialCapital"`
	PositionSize   float64            `json:"positionSize"`
	Commission     float64            `json:"commission"`
	Slippage       float64            `json:"slippage"`
	StrategyIDs    []string           `json:"strategyIds"`
	Error          *string            `json:"error"`
	CreatedAt      string             `json:"createdAt"`
	CompletedAt    *string            `json:"completedAt"`
	Results        []ComparisonResult `json:"results,omitempty"` // detail only
}

// ComparisonService executes and serves grouped strategy comparisons.
type ComparisonService interface {
	ExecuteComparison(ctx context.Context, req ComparisonRequest) (Comparison, error)
	ListComparisons(ctx context.Context) ([]Comparison, error)
	GetComparison(ctx context.Context, id string) (Comparison, error)
}

// WalkForwardRequest is the body of POST /api/walkforwards. Every leg keeps
// a FIXED parameter set across all folds — identical experiment conditions;
// there is no optimization in this version.
type WalkForwardRequest struct {
	Legs           []ComparisonLeg  `json:"legs"`
	AssetID        string           `json:"assetId"`
	Timeframe      market.Timeframe `json:"timeframe"`
	StartDate      string           `json:"startDate"` // overall research range
	EndDate        string           `json:"endDate"`
	InSampleMonths int              `json:"inSampleMonths"`
	OosMonths      int              `json:"oosMonths"`
	StepMonths     int              `json:"stepMonths"` // 0 → defaults to oosMonths
	InitialCapital float64          `json:"initialCapital"`
	PositionSize   float64          `json:"positionSize"`
	Commission     float64          `json:"commission"`
	Slippage       float64          `json:"slippage"`
}

// WalkForwardFold is one (fold index, strategy) member with its boundaries
// and per-window results.
type WalkForwardFold struct {
	FoldIndex  int               `json:"foldIndex"`
	StrategyID string            `json:"strategyId"`
	IsStart    string            `json:"isStart"`
	IsEnd      string            `json:"isEnd"`
	OosStart   string            `json:"oosStart"`
	OosEnd     string            `json:"oosEnd"`
	Status     string            `json:"status"` // pending|completed|failed
	Error      *string           `json:"error,omitempty"`
	IsResult   *ComparisonResult `json:"isResult,omitempty"`
	OosResult  *ComparisonResult `json:"oosResult,omitempty"`
}

// WalkForwardSummary aggregates ONLY the out-of-sample folds for one
// strategy — robustness evidence, never a combined portfolio.
type WalkForwardSummary struct {
	CompletedFolds   int     `json:"completedFolds"`
	FailedFolds      int     `json:"failedFolds"`
	PositiveFolds    int     `json:"positiveFolds"`
	MedianReturnPct  float64 `json:"medianReturnPct"`
	WorstReturnPct   float64 `json:"worstReturnPct"`
	WorstMaxDrawdown float64 `json:"worstMaxDrawdown"`
	TotalTrades      int     `json:"totalTrades"`
	LowTradeFolds    int     `json:"lowTradeFolds"` // <30 trades
	Inconclusive     bool    `json:"inconclusive"`  // most folds under the trade threshold
	// Fold-level risk aggregates over eligible OOS folds — fold trades are
	// never pooled into one account; these are transparent summary stats.
	MedianExpectancy       *float64 `json:"medianExpectancy"`       // across folds with expectancy
	MedianTimeInMarketPct  *float64 `json:"medianTimeInMarketPct"`  // across folds with the metric
	WorstConsecutiveLosses *int     `json:"worstConsecutiveLosses"` // worst single-fold streak
	MissingMetricFolds     int      `json:"missingMetricFolds"`     // completed folds lacking risk metrics
}

// WalkForward is a saved experiment (summary in list, folds on GET).
type WalkForward struct {
	ID             string                         `json:"id"`
	Status         string                         `json:"status"` // pending|running|completed|partial_failed|failed
	AssetID        string                         `json:"assetId"`
	Timeframe      string                         `json:"timeframe"`
	StartDate      string                         `json:"startDate"`
	EndDate        string                         `json:"endDate"`
	InSampleMonths int                            `json:"inSampleMonths"`
	OosMonths      int                            `json:"oosMonths"`
	StepMonths     int                            `json:"stepMonths"`
	InitialCapital float64                        `json:"initialCapital"`
	PositionSize   float64                        `json:"positionSize"`
	Commission     float64                        `json:"commission"`
	Slippage       float64                        `json:"slippage"`
	StrategyIDs    []string                       `json:"strategyIds"`
	Error          *string                        `json:"error"`
	CreatedAt      string                         `json:"createdAt"`
	CompletedAt    *string                        `json:"completedAt"`
	Folds          []WalkForwardFold              `json:"folds,omitempty"`
	Summaries      map[string]*WalkForwardSummary `json:"summaries,omitempty"` // per strategy, OOS only
}

// WalkForwardService runs and serves fixed-configuration walk-forward
// experiments.
type WalkForwardService interface {
	ExecuteWalkForward(ctx context.Context, req WalkForwardRequest) (WalkForward, error)
	PreviewWalkForward(ctx context.Context, req WalkForwardRequest) (WalkForward, error)
	ListWalkForwards(ctx context.Context) ([]WalkForward, error)
	GetWalkForward(ctx context.Context, id string) (WalkForward, error)
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
	// ComparisonID/Sample are set internally by the comparison runner — the
	// public POST endpoint leaves them nil.
	ComparisonID  *string `json:"-"`
	WalkForwardID *string `json:"-"`
	Sample        *string `json:"-"`
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

// --- Strategy graphs ---------------------------------------------------------

// StrategyGraph is a named container of immutable graph revisions.
type StrategyGraph struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	Revisions   int       `json:"revisions"`
	CreatedAt   time.Time `json:"createdAt"`
}

// GraphRevision is one immutable saved definition.
type GraphRevision struct {
	GraphID        string                   `json:"graphId"`
	Revision       int                      `json:"revision"`
	Definition     strategygraph.Definition `json:"definition"`
	DefinitionHash string                   `json:"definitionHash"`
	CreatedAt      time.Time                `json:"createdAt"`
}

// CreateGraphRequest creates a graph with revision 1.
type CreateGraphRequest struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Definition  strategygraph.Definition `json:"definition"`
}

// CreateRevisionRequest saves a new immutable revision.
type CreateRevisionRequest struct {
	Definition strategygraph.Definition `json:"definition"`
}

// ValidateGraphRequest is the body of POST /api/strategy-graphs/validate.
type ValidateGraphRequest struct {
	Definition strategygraph.Definition `json:"definition"`
}

// ValidateGraphResponse reports every validation problem found.
type ValidateGraphResponse struct {
	Valid  bool                       `json:"valid"`
	Errors []strategygraph.FieldError `json:"errors"`
}

// GraphBacktestRequest is the body of the graph backtest endpoint — run
// configuration only; the graph itself always comes from the saved revision.
type GraphBacktestRequest struct {
	AssetID        string           `json:"assetId"`
	Timeframe      market.Timeframe `json:"timeframe"`
	StartDate      string           `json:"startDate"`
	EndDate        string           `json:"endDate"`
	InitialCapital float64          `json:"initialCapital"`
	PositionSize   float64          `json:"positionSize"`
	Commission     float64          `json:"commission"`
	Slippage       float64          `json:"slippage"`
}

// StrategyGraphService manages graphs, revisions, validation and graph runs.
type StrategyGraphService interface {
	ListGraphs(ctx context.Context) ([]StrategyGraph, error)
	ValidateGraph(ctx context.Context, req ValidateGraphRequest) (ValidateGraphResponse, error)
	CreateGraph(ctx context.Context, req CreateGraphRequest) (StrategyGraph, error)
	GetGraph(ctx context.Context, id string) (StrategyGraph, error)
	ListRevisions(ctx context.Context, id string) ([]GraphRevision, error)
	CreateRevision(ctx context.Context, id string, req CreateRevisionRequest) (GraphRevision, error)
	ExecuteGraphBacktest(ctx context.Context, id string, revision int, req GraphBacktestRequest) (BacktestRun, error)
}
