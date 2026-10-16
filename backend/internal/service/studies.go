package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategygraph"
)

const studySpecVersion = "study-spec-v1"

var studyHorizons = []int{1, 5, 10, 20}

func (s *Service) CreateStudy(ctx context.Context, req api.ResearchStudyRequest) (api.ResearchStudy, error) {
	row, err := s.studyRow(ctx, req)
	if err != nil {
		return api.ResearchStudy{}, err
	}
	created, err := s.repos.Studies.CreateStudy(ctx, row)
	if err != nil {
		return api.ResearchStudy{}, err
	}
	return studyDTO(created), nil
}

func (s *Service) UpdateStudy(ctx context.Context, id string, req api.ResearchStudyRequest) (api.ResearchStudy, error) {
	if _, err := s.repos.Studies.GetStudy(ctx, id); errors.Is(err, repository.ErrNotFound) {
		return api.ResearchStudy{}, api.ErrNotFound
	} else if err != nil {
		return api.ResearchStudy{}, err
	}
	row, err := s.studyRow(ctx, req)
	if err != nil {
		return api.ResearchStudy{}, err
	}
	row.ID = id
	updated, err := s.repos.Studies.UpdateStudyDraft(ctx, row)
	if errors.Is(err, repository.ErrNotFound) {
		return api.ResearchStudy{}, api.ErrNotFound
	}
	if errors.Is(err, repository.ErrConflict) {
		return api.ResearchStudy{}, &api.ValidationError{Msg: "only draft studies can be edited"}
	}
	if err != nil {
		return api.ResearchStudy{}, err
	}
	return studyDTO(updated), nil
}

func (s *Service) GetStudy(ctx context.Context, id string) (api.ResearchStudy, error) {
	row, err := s.repos.Studies.GetStudy(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return api.ResearchStudy{}, api.ErrNotFound
	}
	if err != nil {
		return api.ResearchStudy{}, err
	}
	return studyDTO(row), nil
}

func (s *Service) ListStudies(ctx context.Context) ([]api.ResearchStudy, error) {
	rows, err := s.repos.Studies.ListStudies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.ResearchStudy, 0, len(rows))
	for _, row := range rows {
		out = append(out, studyDTO(row))
	}
	return out, nil
}

func (s *Service) studyRow(ctx context.Context, req api.ResearchStudyRequest) (repository.ResearchStudyRow, error) {
	verr := func(msg string) (repository.ResearchStudyRow, error) {
		return repository.ResearchStudyRow{}, &api.ValidationError{Msg: msg}
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Hypothesis = strings.TrimSpace(req.Hypothesis)
	if req.Name == "" || req.Hypothesis == "" {
		return verr("name and hypothesis are required")
	}
	if _, ok := serveCaps[req.Timeframe]; !ok {
		return verr("unsupported timeframe")
	}
	devStart, e1 := time.Parse("2006-01-02", req.DevStart)
	devEnd, e2 := time.Parse("2006-01-02", req.DevEnd)
	holdoutStart, e3 := time.Parse("2006-01-02", req.HoldoutStart)
	holdoutEnd, e4 := time.Parse("2006-01-02", req.HoldoutEnd)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil ||
		!devEnd.After(devStart) || !holdoutEnd.After(holdoutStart) || !holdoutStart.After(devEnd) {
		return verr("valid dev and holdout date ranges are required; holdout must start after dev ends")
	}
	if len(req.Spec.Candidates) == 0 || len(req.Spec.Candidates) > srMaxLegs {
		return verr(fmt.Sprintf("study requires 1 to %d candidates", srMaxLegs))
	}
	if req.Spec.PrimaryMetric != "forward_mean_return" && req.Spec.PrimaryMetric != "forward_event_control_diff" {
		return verr("unsupported primaryMetric")
	}
	if !containsInt(studyHorizons, req.Spec.PrimaryHorizon) {
		return verr("primaryHorizon must be one of 1, 5, 10, or 20")
	}
	if req.Spec.SpecVersion != "" && req.Spec.SpecVersion != studySpecVersion {
		return verr("unsupported specVersion")
	}
	req.Spec.SpecVersion = studySpecVersion
	asset, err := s.repos.Assets.GetAsset(ctx, req.AssetID)
	if errors.Is(err, repository.ErrNotFound) {
		return verr("unknown assetId")
	}
	if err != nil {
		return repository.ResearchStudyRow{}, err
	}
	fingerprint, err := s.data.Fingerprint(asset, req.Timeframe)
	if err != nil {
		return verr(fmt.Sprintf("dataset unavailable: %v", err))
	}
	// Confirm both complete evaluation windows exist and stay within the
	// signal-research resource cap. Warm-up bars are intentionally not subject
	// to reservation checks.
	for _, w := range [][2]time.Time{{devStart, devEnd}, {holdoutStart, holdoutEnd}} {
		candles, err := s.data.Range(asset, req.Timeframe, w[0], w[1].AddDate(0, 0, 1), 0)
		if err != nil || len(candles) == 0 {
			return verr(fmt.Sprintf("no historical data for %s..%s", w[0].Format("2006-01-02"), w[1].Format("2006-01-02")))
		}
		if len(candles) > srMaxEvalBars {
			return verr(fmt.Sprintf("execution window exceeds %d bars", srMaxEvalBars))
		}
	}
	seen := map[string]bool{}
	for i := range req.Spec.Candidates {
		c := &req.Spec.Candidates[i]
		if c.StrategyID == "" || seen[c.StrategyID] {
			return verr("candidate strategyIds must be non-empty and unique")
		}
		seen[c.StrategyID] = true
		st, err := s.resolveStrategy(ctx, c.StrategyID)
		if err != nil {
			var ve *api.ValidationError
			if errors.As(err, &ve) {
				return verr(ve.Msg)
			}
			return repository.ResearchStudyRow{}, err
		}
		if st == nil {
			return verr("unknown candidate strategyId: " + c.StrategyID)
		}
		def := describe(st)
		tfOK, assetOK := len(def.SupportedTimeframes) == 0, len(def.SupportedAssets) == 0
		for _, tf := range def.SupportedTimeframes {
			tfOK = tfOK || tf == req.Timeframe
		}
		for _, supported := range def.SupportedAssets {
			assetOK = assetOK || supported == req.AssetID
		}
		if !tfOK || !assetOK {
			return verr(fmt.Sprintf("%s does not support %s/%s", c.StrategyID, req.AssetID, req.Timeframe))
		}
		if graphID, revision, isGraph := strategygraph.ParseSlug(c.StrategyID); isGraph {
			if c.GraphRevision != nil && *c.GraphRevision != revision {
				return verr("graphRevision must match the immutable candidate strategyId")
			}
			if len(c.Parameters) > 0 {
				return verr("graph candidate parameters are fixed by the saved revision")
			}
			graph, err := s.repos.StrategyGraphs.GetRevision(ctx, graphID, revision)
			if errors.Is(err, repository.ErrNotFound) {
				return verr("unknown graph revision")
			}
			if err != nil {
				return repository.ResearchStudyRow{}, err
			}
			c.GraphRevision = &revision
			c.DefinitionHash = graph.DefinitionHash
			c.Version = def.Version
			c.Parameters = nil
			continue
		}
		if c.GraphRevision != nil {
			return verr("graphRevision is only valid for graph candidates")
		}
		configured, err := configureStrategy(st, c.Parameters)
		if err != nil {
			return verr(err.Error())
		}
		actualDef := describe(configured)
		allowed := map[string]bool{}
		params := make(map[string]any, len(actualDef.Parameters))
		for _, p := range actualDef.Parameters {
			allowed[p.Name] = true
			params[p.Name] = p.Default
		}
		for k := range c.Parameters {
			if !allowed[k] {
				return verr(fmt.Sprintf("unknown parameter %q for %s", k, c.StrategyID))
			}
		}
		for k, v := range c.Parameters {
			params[k] = v
		}
		c.Parameters = params
		c.Version = actualDef.Version
		c.DefinitionHash = ""
	}
	_, hash, err := canonicalStudyHash(req, fingerprint)
	if err != nil {
		return repository.ResearchStudyRow{}, err
	}
	specBytes, err := json.Marshal(req.Spec)
	if err != nil {
		return repository.ResearchStudyRow{}, err
	}
	return repository.ResearchStudyRow{
		Name: req.Name, Description: req.Description, Hypothesis: req.Hypothesis,
		AssetID: req.AssetID, Timeframe: string(req.Timeframe), DatasetFingerprint: fingerprint,
		DevStart: req.DevStart, DevEnd: req.DevEnd, HoldoutStart: req.HoldoutStart, HoldoutEnd: req.HoldoutEnd,
		Spec: specBytes, SpecHash: hash, Executions: []byte("[]"),
	}, nil
}

func canonicalStudyHash(req api.ResearchStudyRequest, fingerprint string) ([]byte, string, error) {
	value := struct {
		Spec               api.StudySpec `json:"spec"`
		AssetID            string        `json:"assetId"`
		Timeframe          string        `json:"timeframe"`
		DatasetFingerprint string        `json:"datasetFingerprint"`
		DevStart           string        `json:"devStart"`
		DevEnd             string        `json:"devEnd"`
		HoldoutStart       string        `json:"holdoutStart"`
		HoldoutEnd         string        `json:"holdoutEnd"`
	}{req.Spec, req.AssetID, string(req.Timeframe), fingerprint, req.DevStart, req.DevEnd, req.HoldoutStart, req.HoldoutEnd}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:]), nil
}

func (s *Service) FreezeStudy(ctx context.Context, id string) (api.ResearchStudy, error) {
	row, err := s.repos.Studies.GetStudy(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return api.ResearchStudy{}, api.ErrNotFound
	}
	if err != nil {
		return api.ResearchStudy{}, err
	}
	if row.Status != "draft" {
		return api.ResearchStudy{}, &api.ValidationError{Msg: "only draft studies can be frozen"}
	}
	var spec api.StudySpec
	if err := json.Unmarshal(row.Spec, &spec); err != nil {
		return api.ResearchStudy{}, err
	}
	candidates := len(spec.Candidates)
	horizons := candidates * len(studyHorizons)
	regimeCells := 0
	if spec.RegimeAnalysis {
		regimeCells = horizons * 8
	}
	counts := api.ResearchCounts{
		Candidates: candidates, ParameterConfigurations: candidates, Horizons: horizons,
		RegimeCells: regimeCells, TotalComparisons: horizons + regimeCells,
		Notice: "this is how many comparisons were looked at; no correction applied",
	}
	for _, c := range spec.Candidates {
		if c.GraphRevision != nil {
			counts.GraphRevisions++
		}
	}
	countsJSON, _ := json.Marshal(counts)
	conflictID, err := s.repos.Studies.FreezeStudy(ctx, id, countsJSON)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return api.ResearchStudy{}, &api.ValidationError{Msg: "study is no longer a draft"}
		}
		return api.ResearchStudy{}, err
	}
	if conflictID != "" {
		conflict, getErr := s.repos.Studies.GetStudy(ctx, conflictID)
		if getErr != nil {
			return api.ResearchStudy{}, getErr
		}
		return api.ResearchStudy{}, &api.ValidationError{Msg: fmt.Sprintf("holdout overlaps reservation by study %q (%s..%s)", conflict.Name, conflict.HoldoutStart, conflict.HoldoutEnd)}
	}
	row, err = s.repos.Studies.GetStudy(ctx, id)
	if err != nil {
		return api.ResearchStudy{}, err
	}
	return studyDTO(row), nil
}

func (s *Service) RunStudyExploratory(ctx context.Context, id string) (api.ResearchStudy, error) {
	return s.executeStudy(ctx, id, "exploratory")
}

func (s *Service) ValidateStudyHoldout(ctx context.Context, id string) (api.ResearchStudy, error) {
	return s.executeStudy(ctx, id, "confirmatory")
}

func (s *Service) executeStudy(ctx context.Context, id, kind string) (api.ResearchStudy, error) {
	row, err := s.repos.Studies.GetStudy(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return api.ResearchStudy{}, api.ErrNotFound
	}
	if err != nil {
		return api.ResearchStudy{}, err
	}
	if kind == "confirmatory" && row.Status == "holdout_consumed" {
		return studyDTO(row), nil
	}
	if kind == "exploratory" && row.Status != "frozen" && row.Status != "exploratory_complete" {
		return api.ResearchStudy{}, &api.ValidationError{Msg: "study must be frozen before exploratory runs"}
	}
	if kind == "confirmatory" && row.Status != "exploratory_complete" {
		return api.ResearchStudy{}, &api.ValidationError{Msg: "study must complete an exploratory run before holdout validation"}
	}
	var spec api.StudySpec
	if err := json.Unmarshal(row.Spec, &spec); err != nil {
		return api.ResearchStudy{}, err
	}
	started := time.Now().UTC()
	window := api.StudyWindow{StartDate: row.DevStart, EndDate: row.DevEnd}
	if kind == "confirmatory" {
		window = api.StudyWindow{StartDate: row.HoldoutStart, EndDate: row.HoldoutEnd}
	}
	req := api.SignalResearchRequest{
		Name: "study " + id + " " + kind, AssetID: row.AssetID, Timeframe: market.Timeframe(row.Timeframe),
		StartDate: window.StartDate, EndDate: window.EndDate, EventStudy: true, RegimeAnalysis: spec.RegimeAnalysis,
	}
	for _, c := range spec.Candidates {
		req.Legs = append(req.Legs, api.SignalResearchLeg{StrategyID: c.StrategyID, Parameters: c.Parameters})
	}
	bypass := ""
	if kind == "confirmatory" {
		bypass = id
	}
	var result api.SignalResearch
	var runErr error
	asset, assetErr := s.repos.Assets.GetAsset(ctx, row.AssetID)
	if assetErr != nil {
		runErr = assetErr
	} else {
		fingerprint, fingerprintErr := s.data.Fingerprint(asset, market.Timeframe(row.Timeframe))
		switch {
		case fingerprintErr != nil:
			runErr = fingerprintErr
		case fingerprint != row.DatasetFingerprint:
			runErr = &api.ValidationError{Msg: "dataset fingerprint changed after the study was created"}
		default:
			result, runErr = s.executeSignalResearch(ctx, req, bypass)
		}
	}
	finished := time.Now().UTC()
	execution := api.StudyExecution{
		ID: newStudyExecutionID(), Kind: kind, StartedAt: started, FinishedAt: finished,
		Window: window, Outcomes: []api.StudyOutcome{},
	}
	nextStatus := row.Status
	if runErr != nil {
		msg := runErr.Error()
		execution.Status, execution.Error = "failed", &msg
	} else {
		execution.Status = "completed"
		execution.SignalResearchID = &result.ID
		execution.Outcomes = studyOutcomes(result, spec)
		if kind == "exploratory" {
			nextStatus = "exploratory_complete"
		} else {
			nextStatus = "holdout_consumed"
		}
	}
	var lastErr *string
	if execution.Error != nil {
		lastErr = execution.Error
	}
	execJSON, err := json.Marshal(execution)
	if err != nil {
		return api.ResearchStudy{}, err
	}
	updated, err := s.repos.Studies.AppendStudyExecution(ctx, id, nextStatus, execJSON, lastErr)
	if errors.Is(err, repository.ErrConflict) {
		return api.ResearchStudy{}, &api.ValidationError{Msg: "study lifecycle changed while execution was running"}
	}
	if err != nil {
		return api.ResearchStudy{}, err
	}
	return studyDTO(updated), nil
}

func studyOutcomes(result api.SignalResearch, spec api.StudySpec) []api.StudyOutcome {
	out := make([]api.StudyOutcome, 0)
	appendHorizon := func(strategyID string, h api.SignalHorizonResult, regimeCell bool) {
		metric := spec.PrimaryMetric
		value := h.MeanReturn
		ciStatus, low, high := "insufficient_data", (*float64)(nil), (*float64)(nil)
		events, controls := h.Eligible, 0
		if h.Benchmark != nil {
			events, controls = h.Benchmark.EventCount, h.Benchmark.ControlCount
		}
		if spec.PrimaryMetric == "forward_event_control_diff" {
			if h.Benchmark == nil {
				value = nil
			} else {
				value = h.Benchmark.DiffMeanControl
				ciStatus, low, high = h.Benchmark.CIStatus, h.Benchmark.CILow, h.Benchmark.CIHigh
			}
		} else {
			ciStatus = "not_computed"
		}
		label := "secondary"
		if !regimeCell && h.Horizon == spec.PrimaryHorizon {
			label = "primary"
		}
		out = append(out, api.StudyOutcome{StrategyID: strategyID, Label: label, Metric: metric,
			Horizon: h.Horizon, Value: value, CIStatus: ciStatus, CILow: low, CIHigh: high,
			Events: events, Controls: controls})
	}
	for _, candidate := range result.Forward {
		for _, horizon := range candidate.Horizons {
			appendHorizon(candidate.StrategyID, horizon, false)
		}
		for _, groups := range [][]api.SignalRegimeForward{candidate.ByTrend, candidate.ByVolatility} {
			for _, group := range groups {
				for _, horizon := range group.Horizons {
					appendHorizon(candidate.StrategyID, horizon, true)
				}
			}
		}
	}
	return out
}

func newStudyExecutionID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Sprintf("exec-%d", time.Now().UnixNano())
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(id[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func containsInt(values []int, value int) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func studyDTO(row repository.ResearchStudyRow) api.ResearchStudy {
	var spec api.StudySpec
	_ = json.Unmarshal(row.Spec, &spec)
	out := api.ResearchStudy{
		ID: row.ID, Status: row.Status, Name: row.Name, Description: row.Description,
		Hypothesis: row.Hypothesis, AssetID: row.AssetID, Timeframe: market.Timeframe(row.Timeframe),
		DatasetFingerprint: row.DatasetFingerprint, DevStart: row.DevStart, DevEnd: row.DevEnd,
		HoldoutStart: row.HoldoutStart, HoldoutEnd: row.HoldoutEnd, Spec: spec, SpecHash: row.SpecHash,
		Executions: []api.StudyExecution{}, LastError: row.LastError,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if len(row.ResearchCounts) > 0 && string(row.ResearchCounts) != "null" {
		var counts api.ResearchCounts
		if json.Unmarshal(row.ResearchCounts, &counts) == nil {
			out.ResearchCounts = &counts
		}
	}
	if len(row.Executions) > 0 {
		_ = json.Unmarshal(row.Executions, &out.Executions)
	}
	if row.FrozenAt != nil {
		v := row.FrozenAt.UTC().Format(time.RFC3339)
		out.FrozenAt = &v
	}
	if row.ExploratoryCompletedAt != nil {
		v := row.ExploratoryCompletedAt.UTC().Format(time.RFC3339)
		out.ExploratoryCompletedAt = &v
	}
	if row.HoldoutConsumedAt != nil {
		v := row.HoldoutConsumedAt.UTC().Format(time.RFC3339)
		out.HoldoutConsumedAt = &v
	}
	return out
}

func (s *Service) checkHoldout(ctx context.Context, assetID string, tf market.Timeframe, start, end, bypassStudyID string) error {
	if s.repos.Studies == nil {
		return nil
	}
	if _, ok := serveCaps[tf]; !ok {
		return nil
	}
	from, e1 := time.Parse("2006-01-02", start)
	to, e2 := time.Parse("2006-01-02", end)
	if e1 != nil || e2 != nil {
		return nil
	}
	reservations, err := s.repos.Studies.ActiveReservations(ctx, assetID, string(tf))
	if err != nil {
		return err
	}
	for _, reservation := range reservations {
		reservedStart, e1 := time.Parse("2006-01-02", reservation.HoldoutStart)
		reservedEnd, e2 := time.Parse("2006-01-02", reservation.HoldoutEnd)
		if e1 != nil || e2 != nil || to.Before(reservedStart) || from.After(reservedEnd) {
			continue
		}
		if reservation.ID == bypassStudyID {
			continue
		}
		return &api.ValidationError{Msg: fmt.Sprintf("request overlaps holdout reserved by study %q (%s..%s)", reservation.Name, reservation.HoldoutStart, reservation.HoldoutEnd)}
	}
	return nil
}

var _ api.ResearchStudyService = (*Service)(nil)
