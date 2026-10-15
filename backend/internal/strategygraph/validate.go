package strategygraph

import (
	"fmt"
	"math"
	"sort"

	"github.com/gold-mine-lab/backend/internal/strategy"
)

// Configurer mirrors the strategy package's optional parameter-binding
// interface without importing an unexported type name.
type Configurer interface {
	Configure(params strategy.Parameters) (strategy.Strategy, error)
}

// Validate checks a graph definition for every structural and semantic error
// it can find (all problems are reported, not just the first). Validation is
// pure: it persists nothing and starts no backtest.
func Validate(d Definition, reg strategy.Registry) []FieldError {
	var errs []FieldError
	add := func(nodeID, msg string) { errs = append(errs, FieldError{NodeID: nodeID, Message: msg}) }

	if len(d.Nodes) == 0 {
		add("", "graph has no nodes")
		return errs
	}
	if len(d.Nodes) > MaxNodes {
		add("", fmt.Sprintf("graph exceeds %d nodes", MaxNodes))
	}
	if len(d.Edges) > MaxEdges {
		add("", fmt.Sprintf("graph exceeds %d edges", MaxEdges))
	}

	byID := make(map[string]*Node, len(d.Nodes))
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.ID == "" {
			add("", "node with empty id")
			continue
		}
		if _, dup := byID[n.ID]; dup {
			add(n.ID, "duplicate node id")
			continue
		}
		byID[n.ID] = n
		switch n.Type {
		case NodeSource:
			if n.StrategyID == "" {
				add(n.ID, "strategy source requires strategyId")
				continue
			}
			base, ok := reg.Get(n.StrategyID)
			if !ok {
				add(n.ID, fmt.Sprintf("unknown strategy %q", n.StrategyID))
				continue
			}
			if c, ok := base.(Configurer); ok {
				if _, err := c.Configure(strategy.Parameters(n.Parameters)); err != nil {
					add(n.ID, fmt.Sprintf("invalid strategy parameters: %s", err.Error()))
				}
			}
		case NodeAnd, NodeOr:
		case NodeThreshold:
			if n.Threshold == nil {
				add(n.ID, "threshold node requires a threshold value")
			} else if math.IsNaN(*n.Threshold) || math.IsInf(*n.Threshold, 0) || *n.Threshold < 0 || *n.Threshold > 1 {
				add(n.ID, "threshold must be a finite number in [0,1]")
			}
		case NodeOutput:
		default:
			add(n.ID, fmt.Sprintf("unknown node type %q", n.Type))
		}
	}

	// Edge integrity + per-node arity.
	inDeg := map[string]int{}
	outDeg := map[string]int{}
	seen := map[Edge]bool{}
	for _, e := range d.Edges {
		if e.From == e.To {
			add(e.From, "self edge")
			continue
		}
		if seen[e] {
			add(e.From, fmt.Sprintf("duplicate edge %s→%s", e.From, e.To))
			continue
		}
		seen[e] = true
		if _, ok := byID[e.From]; !ok {
			add(e.From, fmt.Sprintf("edge references unknown source node %q", e.From))
			continue
		}
		if _, ok := byID[e.To]; !ok {
			add(e.To, fmt.Sprintf("edge references unknown target node %q", e.To))
			continue
		}
		inDeg[e.To]++
		outDeg[e.From]++
	}
	outputs := 0
	for id, n := range byID {
		switch n.Type {
		case NodeSource:
			if inDeg[id] > 0 {
				add(id, "strategy source must not have inputs")
			}
		case NodeAnd, NodeOr:
			if inDeg[id] < 2 {
				add(id, fmt.Sprintf("%s node requires at least 2 inputs", n.Type))
			}
		case NodeThreshold:
			if inDeg[id] != 1 {
				add(id, "threshold node requires exactly 1 input")
			}
		case NodeOutput:
			outputs++
			if inDeg[id] != 1 {
				add(id, "output node requires exactly 1 input")
			}
		}
	}
	if outputs == 0 {
		add("", "graph has no output node")
	} else if outputs > 1 {
		add("", "graph has multiple output nodes")
	}

	// Deterministic topological sort (Kahn, lexicographic ties) doubles as the
	// cycle check.
	order, cyclic := topoOrder(d, inDeg)
	if cyclic != nil {
		add("", "graph contains a cycle")
		return errs // reachability checks are meaningless on a cyclic graph
	}
	_ = order

	// Reachability: every node must be able to contribute to an output.
	reachesOutput := map[string]bool{}
	var outID string
	for id, n := range byID {
		if n.Type == NodeOutput {
			outID = id
			reachesOutput[id] = true
		}
	}
	rev := map[string][]string{}
	for _, e := range d.Edges {
		rev[e.To] = append(rev[e.To], e.From)
	}
	stack := []string{outID}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, p := range rev[cur] {
			if !reachesOutput[p] {
				reachesOutput[p] = true
				stack = append(stack, p)
			}
		}
	}
	sources := 0
	for id, n := range byID {
		if n.Type == NodeSource {
			sources++
		}
		if !reachesOutput[id] && outID != "" {
			add(id, "node cannot contribute to the output")
		}
	}
	if sources == 0 {
		add("", "graph needs at least one strategy source")
	}
	return errs
}

// topoOrder returns nodes in deterministic evaluation order (dependencies
// first, lexicographic on ties). A non-nil second result lists nodes stuck in
// a cycle.
func topoOrder(d Definition, inDeg map[string]int) ([]string, []string) {
	deg := map[string]int{}
	fwd := map[string][]string{}
	for _, n := range d.Nodes {
		deg[n.ID] = inDeg[n.ID]
	}
	for _, e := range d.Edges {
		fwd[e.From] = append(fwd[e.From], e.To)
	}
	var ready []string
	for id, dg := range deg {
		if dg == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		cur := ready[0]
		ready = ready[1:]
		order = append(order, cur)
		for _, nx := range fwd[cur] {
			deg[nx]--
			if deg[nx] == 0 {
				ready = append(ready, nx)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(d.Nodes) {
		stuck := make([]string, 0)
		for id, dg := range deg {
			if dg > 0 {
				stuck = append(stuck, id)
			}
		}
		return order, stuck
	}
	return order, nil
}
