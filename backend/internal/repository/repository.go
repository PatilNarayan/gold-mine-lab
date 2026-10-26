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
	// Provenance — captured at execution time (migration 000002). NULL on
	// legacy rows = pre-2.1 engine that recorded gross P&L only.
	EngineVersion   *string
	StrategyVersion *string
	Dataset         *string
	// Comparison grouping (migration 000003). nil = standalone run.
	ComparisonID *string
	Sample       *string // "in_sample" | "out_of_sample"
	// Walk-forward membership (migration 000004). nil = not a fold run.
	WalkForwardID *string
	// Data provenance (migration 000010): which provider/candles the engine
	// actually consumed. NULL = legacy run whose source is unverifiable.
	DataProvider *string
	DataCandles  *int
	DataFirstBar *time.Time
	DataLastBar  *time.Time
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// ComparisonRow mirrors the comparisons table.
type ComparisonRow struct {
	ID             string
	AssetID        string
	Timeframe      string
	StartDate      string // in-sample window (YYYY-MM-DD)
	EndDate        string
	OosStartDate   *string
	OosEndDate     *string
	InitialCapital float64
	PositionSize   float64
	Commission     float64
	Slippage       float64
	StrategyIDs    []string
	Status         string // pending|running|completed|partial_failed|failed
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

// TradeRow mirrors the backtest_trades table. Pnl is NET of commissions for
// engine ≥2.1 runs; legacy rows have NULL GrossPnl/commissions and store
// gross in Pnl.
type TradeRow struct {
	ID              string
	RunID           string
	StrategyID      string
	AssetID         string
	Side            string
	EntryTime       time.Time
	EntryPrice      float64
	ExitTime        *time.Time
	ExitPrice       *float64
	Quantity        float64
	GrossPnl        *float64
	Pnl             *float64
	EntryCommission *float64
	ExitCommission  *float64
	Status          string
	Result          string
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
	ListRunsByComparison(ctx context.Context, comparisonID string) ([]RunRow, error)
	LatestCompletedRunID(ctx context.Context) (string, error)
}

// ComparisonRepository persists comparison groups and their member runs.
type ComparisonRepository interface {
	// CreateComparison inserts a pending comparison and returns its ID.
	CreateComparison(ctx context.Context, c ComparisonRow) (string, error)
	// SetComparisonStatus transitions status (running→completed|partial_failed|failed)
	// with an optional error message and completed_at.
	SetComparisonStatus(ctx context.Context, id, status string, errMsg *string) error
	GetComparison(ctx context.Context, id string) (ComparisonRow, error)
	ListComparisons(ctx context.Context) ([]ComparisonRow, error)
}

// WalkForwardExperimentRow is one saved walk-forward experiment.
type WalkForwardExperimentRow struct {
	ID             string
	AssetID        string
	Timeframe      string
	RangeStart     string
	RangeEnd       string
	IsMonths       int
	OosMonths      int
	StepMonths     int
	InitialCapital float64
	PositionSize   float64
	Commission     float64
	Slippage       float64
	StrategyIDs    []string
	Status         string
	Error          *string
	CreatedAt      time.Time
	CompletedAt    *time.Time
}

// WalkForwardFoldRow is one (fold, strategy) member: boundaries are fixed at
// creation and run IDs are filled as legs execute.
type WalkForwardFoldRow struct {
	ID           string
	ExperimentID string
	FoldIndex    int
	StrategyID   string
	IsStart      string
	IsEnd        string
	OosStart     string
	OosEnd       string
	IsRunID      *string
	OosRunID     *string
	Status       string
	Error        *string
}

// WalkForwardRepository persists walk-forward experiments and their folds.
type WalkForwardRepository interface {
	CreateExperiment(ctx context.Context, e WalkForwardExperimentRow) (string, error)
	SetExperimentStatus(ctx context.Context, id, status string, errMsg *string) error
	GetExperiment(ctx context.Context, id string) (WalkForwardExperimentRow, error)
	ListExperiments(ctx context.Context) ([]WalkForwardExperimentRow, error)
	CreateFold(ctx context.Context, f WalkForwardFoldRow) (string, error)
	SetFoldResult(ctx context.Context, id, status string, isRunID, oosRunID, errMsg *string) error
	ListFolds(ctx context.Context, experimentID string) ([]WalkForwardFoldRow, error)
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

// StrategyGraphRepository persists named graphs and their immutable
// revisions. CreateRevisionWithStrategy assigns revision = max(existing)+1 and
// writes the materialized strategies row in the same transaction, so a saved
// revision can never exist without its runnable strategy identity.
type StrategyGraphRepository interface {
	ListGraphs(ctx context.Context) ([]StrategyGraphRow, error)
	CreateGraph(ctx context.Context, name, description string) (StrategyGraphRow, error)
	GetGraph(ctx context.Context, id string) (StrategyGraphRow, error)
	ListRevisions(ctx context.Context, graphID string) ([]GraphRevisionRow, error)
	GetRevision(ctx context.Context, graphID string, revision int) (GraphRevisionRow, error)
	CreateRevisionWithStrategy(ctx context.Context, graphID string, definition []byte, hash string, stratFor func(revision int) (strategy.Definition, error)) (GraphRevisionRow, error)
}

// StrategyGraphRow is a graph container.
type StrategyGraphRow struct {
	ID          string
	Name        string
	Description string
	Status      string
	CreatedAt   time.Time
	Revisions   int
}

// GraphRevisionRow is one immutable revision of a graph definition.
type GraphRevisionRow struct {
	GraphID        string
	Revision       int
	Definition     []byte
	DefinitionHash string
	CreatedAt      time.Time
}

type MetricRepository interface {
	GetMetrics(ctx context.Context, runID string) (map[string]float64, error)
	// AggregateMetrics computes sums/means across completed runs.
	AggregateMetrics(ctx context.Context) (netPnl, winRateMean, maxDrawdown float64, totalRuns int, err error)
	// BestStrategyByNetPnl returns the strategy of the completed run with the
	// highest net P&L.
	BestStrategyByNetPnl(ctx context.Context) (id, name string, ok bool, err error)
}

// SignalResearchRow is one persisted cross-strategy analysis: the full
// request plus a summary-only result (per-candle signals are never stored).
type SignalResearchRow struct {
	ID          string
	Status      string
	Request     []byte
	Result      []byte
	Error       *string
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// SignalResearchRepository persists signal-research analyses.
type SignalResearchRepository interface {
	CreateSignalResearch(ctx context.Context, request []byte) (string, error)
	// SetSignalResearchResult writes status/result/error and stamps
	// completed_at for terminal statuses.
	SetSignalResearchResult(ctx context.Context, id, status string, result []byte, errMsg *string) error
	GetSignalResearch(ctx context.Context, id string) (SignalResearchRow, error)
	ListSignalResearch(ctx context.Context) ([]SignalResearchRow, error)
}

var ErrConflict = errors.New("repository: conflict")

type ResearchStudyRow struct {
	ID                     string
	Status                 string
	Name                   string
	Description            string
	Hypothesis             string
	AssetID                string
	Timeframe              string
	DatasetFingerprint     string
	DevStart               string
	DevEnd                 string
	HoldoutStart           string
	HoldoutEnd             string
	Spec                   []byte
	SpecHash               string
	ResearchCounts         []byte
	Executions             []byte
	LastError              *string
	CreatedAt              time.Time
	UpdatedAt              time.Time
	FrozenAt               *time.Time
	ExploratoryCompletedAt *time.Time
	HoldoutConsumedAt      *time.Time
}

type ResearchStudyRepository interface {
	CreateStudy(ctx context.Context, row ResearchStudyRow) (ResearchStudyRow, error)
	UpdateStudyDraft(ctx context.Context, row ResearchStudyRow) (ResearchStudyRow, error)
	GetStudy(ctx context.Context, id string) (ResearchStudyRow, error)
	ListStudies(ctx context.Context) ([]ResearchStudyRow, error)
	FreezeStudy(ctx context.Context, id string, counts []byte) (conflictID string, err error)
	AppendStudyExecution(ctx context.Context, id, newStatus string, executionJSON []byte, errMsg *string) (ResearchStudyRow, error)
	ActiveReservations(ctx context.Context, assetID, timeframe string) ([]ResearchStudyRow, error)
}

// ShadowSessionRow is one forward-test session identity — resumable on
// restart via the open-session unique index.
type ShadowSessionRow struct {
	ID              int64
	StrategyID      string
	StrategyVersion string
	Symbol          string
	Timeframe       string
	Provider        string
	Config          []byte // JSONB snapshot
	StartedAt       time.Time
	LastActiveAt    time.Time
	RestartCount    int
	EndedAt         *time.Time
}

// QuoteObservationRow is one persisted provider quote event.
type QuoteObservationRow struct {
	Seq          int64
	Provider     string
	Symbol       string
	ProviderTime *time.Time
	Bid          *float64
	Ask          *float64
	Mid          *float64
	SpreadPrice  *float64
	SpreadBp     *float64
	Stale        bool
	ReceivedAt   time.Time
	EventKey     *string
}

// QuoteObservationRepository persists recorded quote events.
type QuoteObservationRepository interface {
	InsertQuoteObservations(ctx context.Context, rows []QuoteObservationRow) error
	CountQuoteObservations(ctx context.Context, symbol string) (int, error)
	LatestQuoteObservation(ctx context.Context, symbol string) (*QuoteObservationRow, error)
}

// ShadowOrderRow is one signal→hypothetical-order record (SIMULATED).
type ShadowOrderRow struct {
	ID                int64
	OrderKey          string
	SessionID         *int64
	StrategyID        string
	StrategyVersion   string
	Symbol            string
	Timeframe         string
	Side              string
	SignalBarTime     time.Time
	DecisionAt        time.Time
	Status            string // pending | filled | rejected | expired | missed
	RejectReason      *string
	QuoteProviderTime *time.Time
	QuoteReceivedAt   *time.Time
	QuoteBid          *float64
	QuoteAsk          *float64
	QuoteMid          *float64
	FillPrice         *float64
	FilledAt          *time.Time
	Quantity          *float64
	Commission        *float64
	CreatedAt         time.Time
}

// ShadowPositionRow is the persisted state for one shadow engine —
// open position, realized aggregates, and the restart resume marker.
type ShadowPositionRow struct {
	StrategyID      string
	StrategyVersion string
	Symbol          string
	Timeframe       string
	SessionID       *int64
	Side            *string // NULL = flat
	Quantity        float64
	EntryPrice      *float64
	EntryAt         *time.Time
	EntryOrderID    *int64
	EntryCommission float64
	RealizedPnl     float64
	RealizedComm    float64
	RealizedSpread  float64
	LastBarTime     *time.Time
	UpdatedAt       time.Time
}

// ShadowFillRecord is the persisted detail of one hypothetical execution.
type ShadowFillRecord struct {
	QuoteProviderTime *time.Time
	QuoteReceivedAt   *time.Time
	QuoteBid          *float64
	QuoteAsk          *float64
	QuoteMid          *float64
	FillPrice         float64
	FilledAt          time.Time
	Quantity          float64
	Commission        float64
}
