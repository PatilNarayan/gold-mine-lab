// Package strategy defines the strategy contract: strategies evaluate a candle
// series into a Signal, declare which assets/timeframes they support, and are
// looked up through a Registry. Implementations arrive in later phases.
package strategy

import (
	"context"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
)

// Action is the directional outcome of an evaluation.
type Action string

const (
	ActionBuy     Action = "BUY"
	ActionSell    Action = "SELL"
	ActionNeutral Action = "NEUTRAL"
)

// Signal is the result of a strategy evaluation. JSON tags match the API
// contract (timestamp marshals as RFC3339).
type Signal struct {
	ID         string         `json:"id"`
	StrategyID string         `json:"strategyId"`
	AssetID    string         `json:"assetId"`
	Action     Action         `json:"action"`
	Score      float64        `json:"score"` // 0..1 signal-strength heuristic, strategy-specific — NOT a probability of profit
	Price      float64        `json:"price"` // price the signal refers to
	Timestamp  time.Time      `json:"timestamp"`
	Reason     string         `json:"reason"`
	Metadata   map[string]any `json:"metadata,omitempty"` // strategy-specific extras
}

// ParameterSpec describes one tunable strategy parameter — enough for the API
// to render a form without hard-coding per-strategy fields.
type ParameterSpec struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // "number" | "select" | "boolean"
	Default     any      `json:"default"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Step        *float64 `json:"step,omitempty"`
	Options     []string `json:"options,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Definition is the serializable descriptor of a registered strategy —
// what the API returns for list/detail endpoints.
type Definition struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Description     string   `json:"description"`
	SupportedAssets []string `json:"supportedAssets"`
	// Timeframe is the strategy's DEFAULT timeframe; a run may select any of
	// SupportedTimeframes (per-run choice for cross-timeframe comparison).
	Timeframe           market.Timeframe   `json:"timeframe"`
	SupportedTimeframes []market.Timeframe `json:"supportedTimeframes"`
	Parameters          []ParameterSpec    `json:"parameters"`
	Status              string             `json:"status"` // "active" | "inactive"
}

// Parameters is the resolved parameter value set for one evaluation.
type Parameters map[string]any

// Strategy is a pluggable evaluation rule. Implementations must be safe for
// concurrent use and must not perform I/O inside Evaluate.
type Strategy interface {
	// ID is the stable slug, e.g. "ema-cross".
	ID() string
	// Name is the display name.
	Name() string
	// Version is a semver string; changing logic bumps it.
	Version() string
	// SupportedAssets lists asset IDs this strategy can evaluate.
	SupportedAssets() []string
	// Timeframe is the candle interval the strategy operates on.
	Timeframe() market.Timeframe
	// Parameters returns the parameter schema (defaults + constraints).
	Parameters() []ParameterSpec
	// Evaluate inspects candles (ascending time) and returns the latest signal.
	Evaluate(ctx context.Context, candles []market.Candle) (Signal, error)
}

// f64 is a shorthand for *float64 fields in ParameterSpec literals.
func f64(v float64) *float64 { return &v }

// Registry discovers and stores strategies. Implementations are provided by
// later phases (static registry first, then DB-backed).
type Registry interface {
	// Register adds a strategy; duplicate IDs return an error.
	Register(s Strategy) error
	// Get returns the strategy for an ID, or ok=false.
	Get(id string) (s Strategy, ok bool)
	// List returns every registered strategy in stable order.
	List() []Strategy
}
