// Package strategygraph implements the declarative Strategy Graph domain:
// typed node/edge definitions, server-side validation, and a deterministic
// streaming evaluator that adapts a graph to the existing strategy/backtest
// interfaces. No arbitrary code is ever executed — node semantics are fixed.
//
// Signal semantics across the graph are the shared strategy.Signal model:
// a node's output carries the current candle's timestamp/price plus a
// signal-strength score (NOT a probability of profit).
package strategygraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Node types. The MVP set is deliberately small; semantics below are final.
const (
	NodeSource    = "strategy"  // wraps a registered strategy with its own params
	NodeAnd       = "and"       // all inputs must agree on direction (min score)
	NodeOr        = "or"        // actionable inputs must agree (max score)
	NodeThreshold = "threshold" // passes BUY/SELL whose score >= threshold
	NodeOutput    = "output"    // forwards the single incoming signal
)

// Safety limits keep saved graphs reviewable and evaluation cheap.
const (
	MaxNodes = 32
	MaxEdges = 64
)

// Node is one vertex of the graph.
type Node struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`                 // Node* constants
	StrategyID string         `json:"strategyId,omitempty"` // NodeSource only
	Parameters map[string]any `json:"parameters,omitempty"` // NodeSource only
	Threshold  *float64       `json:"threshold,omitempty"`  // NodeThreshold only, [0,1]
}

// Edge is a directed connection from one node output to one node input.
type Edge struct {
	From string `json:"from"` // source node ID
	To   string `json:"to"`   // target node ID
}

// Definition is the immutable, declarative graph body stored per revision.
type Definition struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// FieldError identifies one validation problem, tied to a node/edge when known.
type FieldError struct {
	NodeID  string `json:"nodeId,omitempty"`
	Message string `json:"message"`
}

func (e FieldError) Error() string {
	if e.NodeID != "" {
		return fmt.Sprintf("node %s: %s", e.NodeID, e.Message)
	}
	return e.Message
}

// ValidationFailed carries the full error list when a definition is invalid.
type ValidationFailed struct {
	Errs []FieldError
}

func (e *ValidationFailed) Error() string {
	msgs := make([]string, 0, len(e.Errs))
	for _, fe := range e.Errs {
		msgs = append(msgs, fe.Error())
	}
	return "invalid graph: " + strings.Join(msgs, "; ")
}

// Hash returns a stable content fingerprint of the definition — node/edge
// ordering does not change the hash. Used for revision provenance.
func Hash(d Definition) string {
	nodes := make([]Node, len(d.Nodes))
	copy(nodes, d.Nodes)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	edges := make([]Edge, len(d.Edges))
	copy(edges, d.Edges)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	b, _ := json.Marshal(struct {
		N []Node `json:"nodes"`
		E []Edge `json:"edges"`
	}{nodes, edges})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
