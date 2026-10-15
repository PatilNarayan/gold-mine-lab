package strategygraph

import (
	"context"
	"fmt"
	"sort"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// GraphStrategy adapts a validated graph revision to the existing
// strategy.Strategy + Streamer + Warmupper contracts so its composite signal
// flows through the SAME backtest engine and accounting as built-ins.
type GraphStrategy struct {
	graphID  string
	revision int
	name     string
	def      Definition
	order    []string          // deterministic topo order
	inEdges  map[string][]Edge // target → incoming edges
	sources  map[string]strategy.Strategy
	assets   []string
	tframes  []market.Timeframe
	warmup   int
	hash     string
}

// StrategySlug is the backtest_runs.strategy_id a graph revision runs under.
func StrategySlug(graphID string, revision int) string {
	return fmt.Sprintf("graph:%s:v%d", graphID, revision)
}

// New validates the definition and builds the strategy. Sources are resolved
// (and parameter-configured) through the shared registry; each source node
// keeps its own configured instance so its streaming state is independent.
func New(reg strategy.Registry, def Definition, graphID, name string, revision int) (*GraphStrategy, error) {
	if errs := Validate(def, reg); len(errs) > 0 {
		return nil, &ValidationFailed{Errs: errs}
	}
	g := &GraphStrategy{
		graphID: graphID, revision: revision, name: name, def: def,
		inEdges: map[string][]Edge{}, sources: map[string]strategy.Strategy{},
		hash: Hash(def),
	}
	assetSet := map[string]bool{}
	tfSet := map[market.Timeframe]bool{}
	first := true
	for _, n := range def.Nodes {
		if n.Type != NodeSource {
			continue
		}
		base, _ := reg.Get(n.StrategyID)
		cfg, err := base.(Configurer).Configure(strategy.Parameters(n.Parameters))
		if err != nil {
			return nil, fmt.Errorf("node %s configure: %w", n.ID, err)
		}
		// A source that can't stream would make evaluation quadratic — the
		// MVP rejects it rather than silently slowing every run.
		if _, ok := cfg.(strategy.Streamer); !ok {
			return nil, fmt.Errorf("node %s: strategy %s does not support streaming evaluation", n.ID, n.StrategyID)
		}
		g.sources[n.ID] = cfg
		if first {
			for _, a := range cfg.SupportedAssets() {
				assetSet[a] = true
			}
			for _, t := range supportedTimeframes(cfg) {
				tfSet[t] = true
			}
			first = false
		} else {
			for a := range assetSet {
				if !contains(cfg.SupportedAssets(), a) {
					delete(assetSet, a)
				}
			}
			st := map[market.Timeframe]bool{}
			for _, t := range supportedTimeframes(cfg) {
				st[t] = true
			}
			for t := range tfSet {
				if !st[t] {
					delete(tfSet, t)
				}
			}
		}
		if w, ok := cfg.(strategy.Warmupper); ok && w.WarmupBars() > g.warmup {
			g.warmup = w.WarmupBars()
		}
	}
	for a := range assetSet {
		g.assets = append(g.assets, a)
	}
	sort.Strings(g.assets)
	for t := range tfSet {
		g.tframes = append(g.tframes, t)
	}
	sort.Slice(g.tframes, func(i, j int) bool { return g.tframes[i] < g.tframes[j] })
	for _, e := range def.Edges {
		g.inEdges[e.To] = append(g.inEdges[e.To], e)
	}
	inDeg := map[string]int{}
	for _, e := range def.Edges {
		inDeg[e.To]++
	}
	order, _ := topoOrder(def, inDeg)
	g.order = order
	return g, nil
}

func supportedTimeframes(s strategy.Strategy) []market.Timeframe {
	if d, ok := s.(interface{ Describe() strategy.Definition }); ok {
		if tf := d.Describe().SupportedTimeframes; len(tf) > 0 {
			return tf
		}
	}
	return []market.Timeframe{s.Timeframe()}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func (g *GraphStrategy) ID() string                { return StrategySlug(g.graphID, g.revision) }
func (g *GraphStrategy) Name() string              { return fmt.Sprintf("%s (graph v%d)", g.name, g.revision) }
func (g *GraphStrategy) Version() string           { return g.hash[:12] }
func (g *GraphStrategy) SupportedAssets() []string { return g.assets }
func (g *GraphStrategy) Timeframe() market.Timeframe {
	if len(g.tframes) > 0 {
		return g.tframes[0]
	}
	return "1d"
}
func (g *GraphStrategy) Parameters() []strategy.ParameterSpec { return []strategy.ParameterSpec{} }
func (g *GraphStrategy) WarmupBars() int                      { return g.warmup }

// Describe exposes capabilities so the service-layer timeframe check sees
// the sources' intersection.
func (g *GraphStrategy) Describe() strategy.Definition {
	return strategy.Definition{
		ID: g.ID(), Name: g.Name(), Version: g.Version(),
		SupportedAssets: g.assets, Timeframe: g.Timeframe(),
		SupportedTimeframes: g.tframes, Parameters: []strategy.ParameterSpec{}, Status: "active",
	}
}

// Evaluate is the batch fallback: replay candles through fresh streams and
// return the final output — causal, never looking ahead.
func (g *GraphStrategy) Evaluate(ctx context.Context, candles []market.Candle) (strategy.Signal, error) {
	st := g.NewStream()
	var last strategy.Signal
	for _, c := range candles {
		if err := ctx.Err(); err != nil {
			return strategy.Signal{}, err
		}
		last = st.Next(c)
	}
	return last, nil
}

// NewStream returns per-run state; each source node owns an independent
// underlying stream so two nodes using the same strategy never share state.
func (g *GraphStrategy) NewStream() strategy.Stream {
	streams := map[string]strategy.Stream{}
	for id, s := range g.sources {
		streams[id] = s.(strategy.Streamer).NewStream()
	}
	return &graphStream{g: g, streams: streams}
}

type graphStream struct {
	g       *GraphStrategy
	streams map[string]strategy.Stream
}

// Next evaluates every node once per candle in topological order.
func (st *graphStream) Next(c market.Candle) strategy.Signal {
	out := map[string]strategy.Signal{}
	var final strategy.Signal
	for _, id := range st.g.order {
		n := nodeByID(st.g.def, id)
		var sig strategy.Signal
		switch n.Type {
		case NodeSource:
			sig = st.streams[id].Next(c)
		default:
			sig = combine(n, st.g.inEdges[id], out, c)
		}
		sig.StrategyID = st.g.ID()
		out[id] = sig
		if n.Type == NodeOutput {
			final = sig
		}
	}
	if final.Metadata == nil {
		final.Metadata = map[string]any{}
	}
	final.Metadata["graphId"] = st.g.graphID
	final.Metadata["graphRevision"] = st.g.revision
	return final
}

func nodeByID(d Definition, id string) Node {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n
		}
	}
	return Node{}
}

// combine implements the operator semantics for non-source nodes.
func combine(n Node, in []Edge, out map[string]strategy.Signal, c market.Candle) strategy.Signal {
	neutral := func(reason string) strategy.Signal {
		return strategy.Signal{Action: strategy.ActionNeutral, Price: c.Close, Timestamp: c.Time, Reason: reason}
	}
	ins := make([]strategy.Signal, 0, len(in))
	for _, e := range in {
		ins = append(ins, out[e.From])
	}
	switch n.Type {
	case NodeAnd:
		action := ins[0].Action
		if action == strategy.ActionNeutral {
			return neutral("and: input neutral")
		}
		min := ins[0].Score
		for _, s := range ins[1:] {
			if s.Action != action {
				return neutral("and: inputs disagree")
			}
			if s.Score < min {
				min = s.Score
			}
		}
		return strategy.Signal{Action: action, Score: min, Price: c.Close, Timestamp: c.Time,
			Reason: fmt.Sprintf("and: all %d inputs %s (weakest score)", len(ins), action)}
	case NodeOr:
		var act *strategy.Signal
		for _, s := range ins {
			if s.Action == strategy.ActionBuy || s.Action == strategy.ActionSell {
				ss := s
				act = &ss
				break
			}
		}
		if act == nil {
			return neutral("or: all inputs neutral")
		}
		action := act.Action
		max := act.Score
		for _, s := range ins {
			if s.Action != strategy.ActionBuy && s.Action != strategy.ActionSell {
				continue
			}
			if s.Action != action {
				return neutral("or: conflicting directions")
			}
			if s.Score > max {
				max = s.Score
			}
		}
		return strategy.Signal{Action: action, Score: max, Price: c.Close, Timestamp: c.Time,
			Reason: fmt.Sprintf("or: actionable inputs agree %s (strongest score)", action)}
	case NodeThreshold:
		s := ins[0]
		if (s.Action == strategy.ActionBuy || s.Action == strategy.ActionSell) && n.Threshold != nil && s.Score >= *n.Threshold {
			return strategy.Signal{Action: s.Action, Score: s.Score, Price: c.Close, Timestamp: c.Time,
				Reason: fmt.Sprintf("threshold: %s score %.2f >= %.2f", s.Action, s.Score, *n.Threshold)}
		}
		return neutral("threshold: below signal-strength threshold or neutral input")
	case NodeOutput:
		s := ins[0]
		return strategy.Signal{Action: s.Action, Score: s.Score, Price: c.Close, Timestamp: c.Time, Reason: s.Reason}
	}
	return neutral("unknown node type")
}
