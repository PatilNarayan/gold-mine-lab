package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/repository"
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
	rev, err := s.repos.StrategyGraphs.CreateRevision(ctx, g.ID, body, strategygraph.Hash(def))
	if err != nil {
		return api.GraphRevision{}, err
	}
	if _, err := s.ensureGraphStrategy(ctx, g.ID, rev.Revision); err != nil {
		return api.GraphRevision{}, err
	}
	return revisionDTO(rev)
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
