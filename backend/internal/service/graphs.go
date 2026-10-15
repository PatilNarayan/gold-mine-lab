package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/internal/strategygraph"
)

// --- Strategy graphs ---------------------------------------------------------
//
// A graph revision is materialised as a strategies row + registry entry under
// the slug `graph:<uuid>:v<rev>` so the existing Execute path — validation,
// fingerprint, engine, accounting, metrics — applies unchanged. The run's
// strategy_id therefore always identifies the exact revision used.

func graphDTO(r repository.StrategyGraphRow) api.StrategyGraph {
	return api.StrategyGraph{ID: r.ID, Name: r.Name, Description: r.Description,
		Status: r.Status, Revisions: r.Revisions, CreatedAt: r.CreatedAt}
}

func revisionDTO(r repository.GraphRevisionRow) (api.GraphRevision, error) {
	var def strategygraph.Definition
	if err := json.Unmarshal(r.Definition, &def); err != nil {
		return api.GraphRevision{}, fmt.Errorf("decode graph definition: %w", err)
	}
	return api.GraphRevision{GraphID: r.GraphID, Revision: r.Revision,
		Definition: def, DefinitionHash: r.DefinitionHash, CreatedAt: r.CreatedAt}, nil
}

func (s *Service) ListGraphs(ctx context.Context) ([]api.StrategyGraph, error) {
	rows, err := s.repos.StrategyGraphs.ListGraphs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.StrategyGraph, 0, len(rows))
	for _, r := range rows {
		out = append(out, graphDTO(r))
	}
	return out, nil
}

func (s *Service) ValidateGraph(ctx context.Context, req api.ValidateGraphRequest) (api.ValidateGraphResponse, error) {
	errs := strategygraph.Validate(req.Definition, s.registry)
	return api.ValidateGraphResponse{Valid: len(errs) == 0, Errors: errs}, nil
}

func (s *Service) CreateGraph(ctx context.Context, req api.CreateGraphRequest) (api.StrategyGraph, error) {
	if req.Name == "" {
		return api.StrategyGraph{}, &api.ValidationError{Msg: "name is required"}
	}
	if err := s.graphValidationErr(req.Definition); err != nil {
		return api.StrategyGraph{}, err
	}
	row, err := s.repos.StrategyGraphs.CreateGraph(ctx, req.Name, req.Description)
	if err != nil {
		return api.StrategyGraph{}, err
	}
	if _, err := s.saveRevision(ctx, row, req.Definition); err != nil {
		return api.StrategyGraph{}, err
	}
	row, err = s.repos.StrategyGraphs.GetGraph(ctx, row.ID)
	if err != nil {
		return api.StrategyGraph{}, err
	}
	return graphDTO(row), nil
}

func (s *Service) GetGraph(ctx context.Context, id string) (api.StrategyGraph, error) {
	row, err := s.repos.StrategyGraphs.GetGraph(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return api.StrategyGraph{}, api.ErrNotFound
	}
	if err != nil {
		return api.StrategyGraph{}, err
	}
	return graphDTO(row), nil
}

func (s *Service) ListRevisions(ctx context.Context, id string) ([]api.GraphRevision, error) {
	if _, err := s.GetGraph(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.repos.StrategyGraphs.ListRevisions(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]api.GraphRevision, 0, len(rows))
	for _, r := range rows {
		d, err := revisionDTO(r)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// CreateRevision validates and stores a NEW immutable revision — it never
// mutates an existing one, so older backtests keep their meaning.
func (s *Service) CreateRevision(ctx context.Context, id string, req api.CreateRevisionRequest) (api.GraphRevision, error) {
	row, err := s.repos.StrategyGraphs.GetGraph(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return api.GraphRevision{}, api.ErrNotFound
	}
	if err != nil {
		return api.GraphRevision{}, err
	}
	if err := s.graphValidationErr(req.Definition); err != nil {
		return api.GraphRevision{}, err
	}
	return s.saveRevision(ctx, row, req.Definition)
}

// ExecuteGraphBacktest runs a saved revision through the standard Execute
// path. The definition comes from the database — never the request body.
func (s *Service) ExecuteGraphBacktest(ctx context.Context, id string, revision int, req api.GraphBacktestRequest) (api.BacktestRun, error) {
	if _, err := s.ensureGraphStrategy(ctx, id, revision); err != nil {
		return api.BacktestRun{}, err
	}
	return s.Execute(ctx, api.BacktestRequest{
		StrategyID: strategygraph.StrategySlug(id, revision),
		AssetID:    req.AssetID, Timeframe: req.Timeframe,
		StartDate: req.StartDate, EndDate: req.EndDate,
		InitialCapital: req.InitialCapital, PositionSize: req.PositionSize,
		Commission: req.Commission, Slippage: req.Slippage,
		Parameters: map[string]any{"graphId": id, "graphRevision": float64(revision)},
	})
}

func (s *Service) graphValidationErr(def strategygraph.Definition) error {
	if errs := strategygraph.Validate(def, s.registry); len(errs) > 0 {
		return &api.ValidationError{Msg: (&strategygraph.ValidationFailed{Errs: errs}).Error()}
	}
	return nil
}

// saveRevision persists the definition as the next revision, then makes the
// revision runnable: a strategies row (FK target + display) and an idempotent
// registry entry.
func (s *Service) saveRevision(ctx context.Context, g repository.StrategyGraphRow, def strategygraph.Definition) (api.GraphRevision, error) {
	body, err := json.Marshal(def)
	if err != nil {
		return api.GraphRevision{}, err
	}
	// Revision row + materialized strategies row commit atomically; the only
	// remaining step (in-memory registry registration) is self-healing via
	// resolveStrategy on any later request.
	rev, err := s.repos.StrategyGraphs.CreateRevisionWithStrategy(ctx, g.ID, body, strategygraph.Hash(def),
		func(revision int) (strategy.Definition, error) {
			gs, err := strategygraph.New(s.registry, def, g.ID, g.Name, revision)
			if err != nil {
				return strategy.Definition{}, err
			}
			return gs.Describe(), nil
		})
	if err != nil {
		return api.GraphRevision{}, err
	}
	if _, err := s.ensureGraphStrategy(ctx, g.ID, rev.Revision); err != nil {
		return api.GraphRevision{}, err
	}
	return revisionDTO(rev)
}

// resolveStrategy returns a strategy for ANY id the engine may see: built-ins
// and already-registered graphs from the registry, or a `graph:<id>:v<n>` slug
// lazily rebuilt from its immutable saved revision. This is what lets generic
// comparison and walk-forward legs reference graphs after a restart without
// pre-loading every revision at boot.
func (s *Service) resolveStrategy(ctx context.Context, id string) (strategy.Strategy, error) {
	if st, ok := s.registry.Get(id); ok {
		return st, nil
	}
	graphID, rev, ok := strategygraph.ParseSlug(id)
	if !ok {
		return nil, nil
	}
	gs, err := s.ensureGraphStrategy(ctx, graphID, rev)
	if err != nil {
		return nil, err
	}
	return gs, nil
}

// graphLegParameters enforces that a graph leg's node parameters come ONLY
// from the immutable saved revision: client-supplied parameters on a graph
// leg are rejected, and the run carries graphId/graphRevision provenance.
func graphLegParameters(strategyID string, params map[string]any) (map[string]any, error) {
	graphID, rev, ok := strategygraph.ParseSlug(strategyID)
	if !ok {
		return params, nil
	}
	if len(params) > 0 {
		return nil, &api.ValidationError{Msg: "graph leg parameters are fixed by the saved revision and cannot be overridden"}
	}
	return map[string]any{"graphId": graphID, "graphRevision": float64(rev)}, nil
}

// ensureGraphStrategy builds the adapter for a saved revision and registers
// it (idempotently) so Execute's registry lookup succeeds.
func (s *Service) ensureGraphStrategy(ctx context.Context, graphID string, revision int) (*strategygraph.GraphStrategy, error) {
	slug := strategygraph.StrategySlug(graphID, revision)
	if st, ok := s.registry.Get(slug); ok {
		if gs, ok := st.(*strategygraph.GraphStrategy); ok {
			return gs, nil
		}
	}
	g, err := s.repos.StrategyGraphs.GetGraph(ctx, graphID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, api.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rev, err := s.repos.StrategyGraphs.GetRevision(ctx, graphID, revision)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, api.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var def strategygraph.Definition
	if err := json.Unmarshal(rev.Definition, &def); err != nil {
		return nil, fmt.Errorf("decode graph definition: %w", err)
	}
	gs, err := strategygraph.New(s.registry, def, graphID, g.Name, revision)
	if err != nil {
		var vf *strategygraph.ValidationFailed
		if errors.As(err, &vf) {
			return nil, &api.ValidationError{Msg: vf.Error()}
		}
		return nil, err
	}
	// strategies row so backtest_runs.strategy_id FK + name resolution work.
	if err := s.repos.Strategies.UpsertStrategy(ctx, gs.Describe()); err != nil {
		return nil, fmt.Errorf("register graph strategy row: %w", err)
	}
	if err := s.registry.Register(gs); err != nil {
		if _, ok := s.registry.Get(slug); !ok {
			return nil, fmt.Errorf("register graph strategy: %w", err)
		} // concurrent registration already present — fine
	}
	return gs, nil
}

// --- Graph diagnostics (read-only signal inspector) --------------------------

const (
	diagDefaultTraceLimit = 200
	diagMaxTraceLimit     = 2000
	diagMaxEvalBars       = 250_000
)

// GraphDiagnostics replays a saved revision's OWN streams over a historical
// window and reports what every node emitted — read-only: no runs, no trades,
// no persistence. Warm-up candles feed the sources but are never counted.
func (s *Service) GraphDiagnostics(ctx context.Context, id string, revision int, req api.GraphDiagnosticsRequest) (api.GraphDiagnostics, error) {
	verr := func(m string) (api.GraphDiagnostics, error) {
		return api.GraphDiagnostics{}, &api.ValidationError{Msg: m}
	}
	gs, err := s.ensureGraphStrategy(ctx, id, revision)
	if err != nil {
		return api.GraphDiagnostics{}, err
	}
	asset, err := s.repos.Assets.GetAsset(ctx, req.AssetID)
	if errors.Is(err, repository.ErrNotFound) {
		return verr("unknown assetId")
	}
	if err != nil {
		return api.GraphDiagnostics{}, err
	}
	if _, ok := serveCaps[req.Timeframe]; !ok {
		return verr("unsupported timeframe")
	}
	def := gs.Describe()
	supported := false
	for _, a := range def.SupportedAssets {
		if a == req.AssetID {
			supported = true
		}
	}
	if !supported {
		return verr("graph does not support this asset")
	}
	tfOK := false
	for _, t := range def.SupportedTimeframes {
		if t == req.Timeframe {
			tfOK = true
		}
	}
	if !tfOK {
		return verr(fmt.Sprintf("timeframe %s is not supported by this graph revision (supported: %v)", req.Timeframe, def.SupportedTimeframes))
	}
	start, e1 := time.Parse("2006-01-02", req.StartDate)
	end, e2 := time.Parse("2006-01-02", req.EndDate)
	if e1 != nil || e2 != nil || !end.After(start) {
		return verr("invalid startDate/endDate")
	}
	limit := req.TraceLimit
	if limit < 0 {
		return verr("traceLimit must be >= 0")
	}
	if limit == 0 {
		limit = diagDefaultTraceLimit
	}
	if limit > diagMaxTraceLimit {
		return verr(fmt.Sprintf("traceLimit exceeds maximum %d", diagMaxTraceLimit))
	}
	to := end.AddDate(0, 0, 1)
	warmup := gs.WarmupBars()
	candles, err := s.data.Range(asset, req.Timeframe, start, to, warmup)
	if err != nil {
		return verr(fmt.Sprintf("no candle data for window: %v", err))
	}
	windowStart := 0
	for windowStart < len(candles) && candles[windowStart].Time.Before(start) {
		windowStart++
	}
	evalBars := len(candles) - windowStart
	if evalBars < 1 {
		return verr("no candles inside the requested window")
	}
	if evalBars > diagMaxEvalBars {
		return verr(fmt.Sprintf("window evaluates %d candles, exceeding the %d limit — narrow the date range", evalBars, diagMaxEvalBars))
	}

	ts, ok := gs.NewStream().(strategygraph.TraceStreamer)
	if !ok {
		return api.GraphDiagnostics{}, fmt.Errorf("graph revision does not support tracing")
	}
	type acc struct {
		sum     api.GraphNodeSummary
		sc      float64
		reasons map[string]int
	}
	accs := map[string]*acc{}
	traces := make([]api.GraphTraceRow, 0, min(limit, evalBars))
	truncated := false
	for i, c := range candles {
		if err := ctx.Err(); err != nil {
			return api.GraphDiagnostics{}, err
		}
		_, nodes := ts.NextWithTrace(c)
		if i < windowStart {
			continue // warm-up: feeds sources, never counted
		}
		row := api.GraphTraceRow{Timestamp: c.Time.UTC().Format("2006-01-02T15:04:05Z")}
		for _, nt := range nodes {
			a := accs[nt.NodeID]
			if a == nil {
				a = &acc{sum: api.GraphNodeSummary{NodeID: nt.NodeID, NodeType: nt.NodeType}, reasons: map[string]int{}}
				accs[nt.NodeID] = a
			}
			a.sum.EvaluatedBars++
			switch nt.Action {
			case string(strategy.ActionBuy):
				a.sum.Buy++
				a.sc += nt.Score
			case string(strategy.ActionSell):
				a.sum.Sell++
				a.sc += nt.Score
			default:
				a.sum.Neutral++
			}
			a.reasons[nt.Reason]++
			row.Nodes = append(row.Nodes, api.GraphTraceEntry{NodeID: nt.NodeID, Action: nt.Action, Score: nt.Score, Reason: nt.Reason})
		}
		if len(traces) < limit {
			traces = append(traces, row)
		} else {
			truncated = true
		}
	}
	out := api.GraphDiagnostics{
		GraphID: id, Revision: revision, Hash: gs.Version(),
		AssetID: req.AssetID, Timeframe: req.Timeframe,
		StartDate: req.StartDate, EndDate: req.EndDate,
		EvaluatedBars: evalBars, WarmupBars: warmup,
		TraceLimit: limit, Truncated: truncated, Traces: traces,
	}
	if fp, ferr := s.data.Fingerprint(asset, req.Timeframe); ferr == nil {
		out.Dataset = &fp
	}
	if evalBars > 0 {
		lc := candles[len(candles)-1].Time.UTC().Format("2006-01-02T15:04:05Z")
		out.LastCandle = &lc
	}
	for _, nid := range gs.Order() {
		a := accs[nid]
		if a == nil {
			continue
		}
		if a.sum.EvaluatedBars > 0 {
			a.sum.ActionablePct = float64(a.sum.Buy+a.sum.Sell) / float64(a.sum.EvaluatedBars) * 100
		}
		if act := a.sum.Buy + a.sum.Sell; act > 0 {
			avg := a.sc / float64(act)
			a.sum.AvgScore = &avg
		}
		a.sum.TopReasons = topReasons(a.reasons, 3)
		out.Nodes = append(out.Nodes, a.sum)
	}
	return out, nil
}

func topReasons(m map[string]int, n int) []api.ReasonCount {
	all := make([]api.ReasonCount, 0, len(m))
	for r, c := range m {
		all = append(all, api.ReasonCount{Reason: r, Count: c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Count != all[j].Count {
			return all[i].Count > all[j].Count
		}
		return all[i].Reason < all[j].Reason
	})
	if len(all) > n {
		all = all[:n]
	}
	return all
}
