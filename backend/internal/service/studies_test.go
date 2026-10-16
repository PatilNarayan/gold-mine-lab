package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
)

func TestStudyRejectsInvalidSpecificationsBeforeDataAccess(t *testing.T) {
	valid := hashRequest()
	tests := map[string]func(*api.ResearchStudyRequest){
		"name":                     func(r *api.ResearchStudyRequest) { r.Name = " " },
		"hypothesis":               func(r *api.ResearchStudyRequest) { r.Hypothesis = "" },
		"no candidates":            func(r *api.ResearchStudyRequest) { r.Spec.Candidates = nil },
		"unsupported metric":       func(r *api.ResearchStudyRequest) { r.Spec.PrimaryMetric = "sharpe" },
		"unsupported horizon":      func(r *api.ResearchStudyRequest) { r.Spec.PrimaryHorizon = 2 },
		"overlapping windows":      func(r *api.ResearchStudyRequest) { r.HoldoutStart = r.DevEnd },
		"unsupported spec version": func(r *api.ResearchStudyRequest) { r.Spec.SpecVersion = "future" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := valid
			req.Spec.Candidates = append([]api.StudyCandidate(nil), valid.Spec.Candidates...)
			mutate(&req)
			_, err := (&Service{}).studyRow(context.Background(), req)
			var validation *api.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

func hashRequest() api.ResearchStudyRequest {
	return api.ResearchStudyRequest{
		AssetID: "xauusd", Timeframe: "1d", DevStart: "2015-01-01", DevEnd: "2020-12-31",
		HoldoutStart: "2021-01-01", HoldoutEnd: "2022-12-31",
		Spec: api.StudySpec{SpecVersion: studySpecVersion, PrimaryMetric: "forward_mean_return",
			PrimaryHorizon: 10, Candidates: []api.StudyCandidate{
				{StrategyID: "ema-cross", Parameters: map[string]any{"fast_period": float64(12), "slow_period": float64(26)}},
			}},
	}
}

func TestCanonicalStudyHashIsDeterministicAndCoversPlan(t *testing.T) {
	base := hashRequest()
	_, expected, err := canonicalStudyHash(base, "xauusd:1d@sha256:dataset")
	if err != nil {
		t.Fatal(err)
	}
	reordered := hashRequest()
	reordered.Spec.Candidates[0].Parameters = map[string]any{"slow_period": float64(26), "fast_period": float64(12)}
	_, actual, err := canonicalStudyHash(reordered, "xauusd:1d@sha256:dataset")
	if err != nil || actual != expected {
		t.Fatalf("map-key order changed hash: got %q, want %q (err %v)", actual, expected, err)
	}

	mutations := map[string]func(*api.ResearchStudyRequest){
		"candidate":  func(r *api.ResearchStudyRequest) { r.Spec.Candidates[0].StrategyID = "rsi-mean-reversion" },
		"parameters": func(r *api.ResearchStudyRequest) { r.Spec.Candidates[0].Parameters["fast_period"] = float64(13) },
		"metric":     func(r *api.ResearchStudyRequest) { r.Spec.PrimaryMetric = "forward_event_control_diff" },
		"horizon":    func(r *api.ResearchStudyRequest) { r.Spec.PrimaryHorizon = 20 },
		"window":     func(r *api.ResearchStudyRequest) { r.HoldoutStart = "2021-02-01" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			req := hashRequest()
			mutate(&req)
			_, got, err := canonicalStudyHash(req, "xauusd:1d@sha256:dataset")
			if err != nil {
				t.Fatal(err)
			}
			if got == expected {
				t.Fatalf("%s change did not alter hash", name)
			}
		})
	}
}

type studyReservationRepo struct{ rows []repository.ResearchStudyRow }

func (r *studyReservationRepo) CreateStudy(context.Context, repository.ResearchStudyRow) (repository.ResearchStudyRow, error) {
	return repository.ResearchStudyRow{}, errors.New("unused")
}
func (r *studyReservationRepo) UpdateStudyDraft(context.Context, repository.ResearchStudyRow) (repository.ResearchStudyRow, error) {
	return repository.ResearchStudyRow{}, errors.New("unused")
}
func (r *studyReservationRepo) GetStudy(context.Context, string) (repository.ResearchStudyRow, error) {
	return repository.ResearchStudyRow{}, repository.ErrNotFound
}
func (r *studyReservationRepo) ListStudies(context.Context) ([]repository.ResearchStudyRow, error) {
	return nil, nil
}
func (r *studyReservationRepo) FreezeStudy(context.Context, string, []byte) (string, error) {
	return "", errors.New("unused")
}
func (r *studyReservationRepo) AppendStudyExecution(context.Context, string, string, []byte, *string) (repository.ResearchStudyRow, error) {
	return repository.ResearchStudyRow{}, errors.New("unused")
}
func (r *studyReservationRepo) ActiveReservations(_ context.Context, assetID, tf string) ([]repository.ResearchStudyRow, error) {
	var out []repository.ResearchStudyRow
	for _, row := range r.rows {
		if row.AssetID == assetID && row.Timeframe == tf &&
			(row.Status == "frozen" || row.Status == "exploratory_complete") {
			out = append(out, row)
		}
	}
	return out, nil
}

func TestCheckHoldoutInclusiveOverlapAndOwningBypass(t *testing.T) {
	dir := t.TempDir()
	for _, file := range []string{"XAU_1d_data.csv", "XAU_4h_data.csv"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	asset := market.Asset{ID: "xauusd", Symbol: "XAUUSD", QuoteCurrency: "USD"}
	data := market.NewCSVProvider(dir, nil)
	fingerprint, err := data.Fingerprint(asset, market.Timeframe1d)
	if err != nil {
		t.Fatal(err)
	}
	repo := &studyReservationRepo{rows: []repository.ResearchStudyRow{{
		ID: "owner", Status: "frozen", Name: "reserved", AssetID: asset.ID, Timeframe: "1d",
		DatasetFingerprint: fingerprint, HoldoutStart: "2021-01-01", HoldoutEnd: "2021-12-31",
	}}}
	svc := &Service{repos: Repositories{Assets: fakeAssetsRepo{asset}, Studies: repo}, data: data}
	tests := []struct {
		name, start, end string
		tf               market.Timeframe
		bypass           string
		blocked          bool
	}{
		{name: "partial overlap", start: "2020-12-20", end: "2021-01-10", tf: "1d", blocked: true},
		{name: "contained", start: "2021-02-01", end: "2021-05-01", tf: "1d", blocked: true},
		{name: "shared start endpoint", start: "2020-12-01", end: "2021-01-01", tf: "1d", blocked: true},
		{name: "shared end endpoint", start: "2021-12-31", end: "2022-01-10", tf: "1d", blocked: true},
		{name: "owning study bypass", start: "2021-02-01", end: "2021-05-01", tf: "1d", bypass: "owner"},
		{name: "other id cannot bypass", start: "2021-02-01", end: "2021-05-01", tf: "1d", bypass: "other", blocked: true},
		{name: "different timeframe", start: "2021-02-01", end: "2021-05-01", tf: "4h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.checkHoldout(context.Background(), asset.ID, tt.tf, tt.start, tt.end, tt.bypass)
			if tt.blocked != (err != nil) {
				t.Fatalf("checkHoldout err = %v, blocked=%v", err, tt.blocked)
			}
		})
	}
	repo.rows[0].DatasetFingerprint = "different-dataset"
	if err := svc.checkHoldout(context.Background(), asset.ID, market.Timeframe1d, "2021-02-01", "2021-05-01", ""); err == nil {
		t.Fatal("a changed dataset fingerprint released an active date reservation")
	}
	repo.rows[0].Status = "holdout_consumed"
	if err := svc.checkHoldout(context.Background(), asset.ID, market.Timeframe1d, "2021-02-01", "2021-05-01", ""); err != nil {
		t.Fatalf("consumed study retained reservation: %v", err)
	}
}

func TestStudySpecCanonicalJSONKeepsCandidateOrder(t *testing.T) {
	req := hashRequest()
	second := api.StudyCandidate{StrategyID: "rsi-mean-reversion", Parameters: map[string]any{"rsi_period": float64(14)}}
	req.Spec.Candidates = append(req.Spec.Candidates, second)
	b1, _, err := canonicalStudyHash(req, "fp")
	if err != nil {
		t.Fatal(err)
	}
	req.Spec.Candidates[0], req.Spec.Candidates[1] = req.Spec.Candidates[1], req.Spec.Candidates[0]
	b2, _, err := canonicalStudyHash(req, "fp")
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal(b1, &a)
	_ = json.Unmarshal(b2, &b)
	if string(b1) == string(b2) {
		t.Fatal("candidate ordering was normalized away")
	}
}

func TestStudyOutcomesUseMetricMatchedValueAndInterval(t *testing.T) {
	eventMean, diff, low, high := 0.12, 0.04, -0.01, 0.09
	result := api.SignalResearch{Forward: []api.SignalForwardResult{{
		StrategyID: "ema-cross",
		Horizons: []api.SignalHorizonResult{{
			Horizon: 10, Eligible: 20, MeanReturn: &eventMean,
			Benchmark: &api.SignalHorizonBenchmark{
				EventCount: 20, ControlCount: 30, DiffMeanControl: &diff,
				CIStatus: "available", CILow: &low, CIHigh: &high,
			},
		}},
	}}}
	meanSpec := api.StudySpec{PrimaryMetric: "forward_mean_return", PrimaryHorizon: 10}
	mean := studyOutcomes(result, meanSpec)[0]
	if mean.Value != &eventMean || mean.CIStatus != "not_computed" || mean.CILow != nil || mean.CIHigh != nil {
		t.Fatalf("mean-return outcome has mismatched benchmark interval: %+v", mean)
	}
	diffSpec := api.StudySpec{PrimaryMetric: "forward_event_control_diff", PrimaryHorizon: 10}
	contrast := studyOutcomes(result, diffSpec)[0]
	if contrast.Value != &diff || contrast.CIStatus != "available" || contrast.CILow != &low || contrast.CIHigh != &high {
		t.Fatalf("event-control outcome lost its paired bootstrap interval: %+v", contrast)
	}
}
