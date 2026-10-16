//go:build integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/config"
	"github.com/gold-mine-lab/backend/internal/database"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository/postgres"
	"github.com/gold-mine-lab/backend/internal/service"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/internal/strategygraph"
	"github.com/gold-mine-lab/backend/migrations"
)

func executionCount(t *testing.T, data []byte) int {
	t.Helper()
	var executions []json.RawMessage
	if err := json.Unmarshal(data, &executions); err != nil {
		t.Fatalf("decode execution history: %v", err)
	}
	return len(executions)
}

func TestResearchStudyGovernanceAgainstPostgres(t *testing.T) {
	dbURL, dataDir := os.Getenv("DATABASE_URL"), os.Getenv("MARKET_DATA_DIR")
	if dbURL == "" || dataDir == "" {
		t.Skip("DATABASE_URL / MARKET_DATA_DIR unset")
	}
	if err := migrations.Up(dbURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	pool, err := database.NewPool(ctx, config.DBConfig{URL: dbURL, MaxConns: 8, MinConns: 1})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	store := postgres.New(pool.Pool)
	reg := strategy.NewRegistry()
	for _, st := range []strategy.Strategy{strategy.NewEMACross(), strategy.NewRSIMeanReversion(), strategy.NewDonchianBreakout()} {
		if err := reg.Register(st); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertStrategy(ctx, st.(interface{ Describe() strategy.Definition }).Describe()); err != nil {
			t.Fatalf("seed strategy: %v", err)
		}
	}
	if err := store.UpsertAsset(ctx, service.SeedAsset); err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	svc := service.New(service.Repositories{
		Assets: store, Strategies: store, Signals: store, Runs: store, Trades: store,
		Metrics: store, Comparisons: store, WalkForwards: store, StrategyGraphs: store,
		SignalResearch: store, Studies: store,
	}, market.NewCSVProvider(dataDir, nil), reg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	prefix := fmt.Sprintf("p12-integration-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Pool.Exec(context.Background(), `DELETE FROM research_studies WHERE name LIKE $1`, prefix+"%")
		pool.Close()
	})
	router := api.NewRouter(api.Deps{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), Health: pool, Market: svc,
		Strategies: svc, Backtests: svc, Trades: svc, Comparisons: svc, WalkForwards: svc,
		StrategyGraphs: svc, SignalResearch: svc, Studies: svc,
		AllowedOrigins: []string{"http://localhost:5199"}, Env: "test",
	})
	oldRuns, err := svc.ListRuns(ctx, "")
	if err != nil {
		t.Fatalf("list existing runs: %v", err)
	}
	oldComparisons, err := svc.ListComparisons(ctx)
	if err != nil {
		t.Fatalf("list existing comparisons: %v", err)
	}
	oldResearch, err := svc.ListSignalResearch(ctx)
	if err != nil {
		t.Fatalf("list existing signal research: %v", err)
	}

	makeRequest := func(name string) api.ResearchStudyRequest {
		return api.ResearchStudyRequest{
			Name: name, Description: "integration exercise", Hypothesis: "Recorded candidate outcomes differ.",
			AssetID: "xauusd", Timeframe: "1d",
			DevStart: "2015-01-01", DevEnd: "2020-12-31",
			HoldoutStart: "2021-01-01", HoldoutEnd: "2022-12-31",
			Spec: api.StudySpec{
				Candidates: []api.StudyCandidate{
					{StrategyID: "donchian-breakout", Parameters: map[string]any{}},
					{StrategyID: "ema-cross", Parameters: map[string]any{"fast_period": float64(12), "slow_period": float64(26)}},
				},
				PrimaryMetric: "forward_event_control_diff", PrimaryHorizon: 10,
				RegimeAnalysis: true, SpecVersion: "study-spec-v1",
			},
		}
	}
	mainStudy, err := svc.CreateStudy(ctx, makeRequest(prefix+"-main"))
	if err != nil {
		t.Fatalf("create study: %v", err)
	}
	if len(mainStudy.Spec.Candidates) != 2 || mainStudy.Spec.PrimaryMetric != "forward_event_control_diff" {
		t.Fatalf("created study returned an incomplete normalized spec: %+v", mainStudy.Spec)
	}
	conflictStudy, err := svc.CreateStudy(ctx, makeRequest(prefix+"-conflict"))
	if err != nil {
		t.Fatalf("create conflicting study: %v", err)
	}
	var wg sync.WaitGroup
	freezeErrs := make([]error, 2)
	for i, id := range []string{mainStudy.ID, conflictStudy.ID} {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			_, freezeErrs[i] = svc.FreezeStudy(ctx, id)
		}(i, id)
	}
	wg.Wait()
	winners := 0
	winnerID := ""
	for i, err := range freezeErrs {
		if err == nil {
			winners++
			winnerID = []string{mainStudy.ID, conflictStudy.ID}[i]
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent overlapping freezes succeeded %d times; errors=%v", winners, freezeErrs)
	}
	loserID := mainStudy.ID
	if winnerID == mainStudy.ID {
		loserID = conflictStudy.ID
	}
	if _, err := svc.RunStudyExploratory(ctx, loserID); err == nil {
		t.Fatal("draft study accepted an exploratory run")
	}
	if _, err := svc.ValidateStudyHoldout(ctx, loserID); err == nil {
		t.Fatal("draft study accepted confirmatory validation")
	}
	winner, err := svc.GetStudy(ctx, winnerID)
	if err != nil || winner.Status != "frozen" {
		t.Fatalf("winning study status %q, err=%v", winner.Status, err)
	}
	if _, err := svc.UpdateStudy(ctx, winnerID, makeRequest(prefix+"-edit")); err == nil {
		t.Fatal("frozen study accepted a PATCH")
	}
	failedMessage := "injected exploratory failure for lifecycle check"
	failedExploratory, err := store.AppendStudyExecution(ctx, winnerID, "frozen",
		[]byte(`{"id":"failed-exp","kind":"exploratory","status":"failed","outcomes":[]}`), &failedMessage)
	if err != nil || failedExploratory.Status != "frozen" || executionCount(t, failedExploratory.Executions) != 1 {
		t.Fatalf("failed exploratory execution advanced lifecycle: study=%+v err=%v", failedExploratory, err)
	}

	// Action endpoints explicitly reject caller-supplied run-affecting fields.
	actionReq := httptest.NewRequest(http.MethodPost, "/api/research-studies/"+conflictStudy.ID+"/freeze", bytes.NewBufferString(`{"candidates":[]}`))
	actionReq.Header.Set("Content-Type", "application/json")
	actionRes := httptest.NewRecorder()
	router.ServeHTTP(actionRes, actionReq)
	if actionRes.Code != http.StatusBadRequest {
		t.Fatalf("freeze with supplied body returned HTTP %d, want 400", actionRes.Code)
	}

	graph, err := svc.CreateGraph(ctx, api.CreateGraphRequest{
		Name: prefix + "-graph",
		Definition: strategygraph.Definition{
			Nodes: []strategygraph.Node{
				{ID: "source", Type: strategygraph.NodeSource, StrategyID: "ema-cross"},
				{ID: "out", Type: strategygraph.NodeOutput},
			},
			Edges: []strategygraph.Edge{{From: "source", To: "out"}},
		},
	})
	if err != nil {
		t.Fatalf("create diagnostics graph: %v", err)
	}

	postJSON := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	assertBlocked := func(name, path string, body any) {
		t.Helper()
		res := postJSON(path, body)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s returned HTTP %d, want 400: %s", name, res.Code, res.Body.String())
		}
	}
	assertBlocked("backtest", "/api/backtests", api.BacktestRequest{
		StrategyID: "ema-cross", AssetID: "xauusd", Timeframe: "1d",
		StartDate: "2021-03-01", EndDate: "2021-03-31",
		InitialCapital: 10000, PositionSize: 5, Commission: 0, Slippage: 0,
		Parameters: map[string]any{},
	})
	oosStart, oosEnd := "2021-03-01", "2021-03-31"
	assertBlocked("comparison OOS window", "/api/comparisons", api.ComparisonRequest{
		Legs:    []api.ComparisonLeg{{StrategyID: "ema-cross"}, {StrategyID: "rsi-mean-reversion"}},
		AssetID: "xauusd", Timeframe: "1d", StartDate: "2020-01-01", EndDate: "2020-06-30",
		OutOfSampleStartDate: &oosStart, OutOfSampleEndDate: &oosEnd,
		InitialCapital: 10000, PositionSize: 5,
	})
	assertBlocked("walk-forward", "/api/walkforwards", api.WalkForwardRequest{
		Legs: []api.ComparisonLeg{{StrategyID: "ema-cross"}}, AssetID: "xauusd", Timeframe: "1d",
		StartDate: "2021-01-01", EndDate: "2022-12-31", InSampleMonths: 3, OosMonths: 1,
		InitialCapital: 10000, PositionSize: 5,
	})
	assertBlocked("signal research", "/api/signal-research", api.SignalResearchRequest{
		Legs:    []api.SignalResearchLeg{{StrategyID: "ema-cross"}, {StrategyID: "rsi-mean-reversion"}},
		AssetID: "xauusd", Timeframe: "1d", StartDate: "2021-06-01", EndDate: "2021-12-31", EventStudy: true,
	})
	assertBlocked("graph diagnostics", fmt.Sprintf("/api/strategy-graphs/%s/versions/1/diagnostics", graph.ID), api.GraphDiagnosticsRequest{
		AssetID: "xauusd", Timeframe: "1d", StartDate: "2021-03-01", EndDate: "2021-03-31",
	})

	explore := httptest.NewRequest(http.MethodPost, "/api/research-studies/"+winnerID+"/exploratory-runs", nil)
	exploreRes := httptest.NewRecorder()
	router.ServeHTTP(exploreRes, explore)
	if exploreRes.Code != http.StatusOK {
		t.Fatalf("exploratory run returned HTTP %d: %s", exploreRes.Code, exploreRes.Body.String())
	}
	var afterExplore api.ResearchStudy
	if err := json.Unmarshal(exploreRes.Body.Bytes(), &afterExplore); err != nil || afterExplore.Status != "exploratory_complete" {
		t.Fatalf("exploratory status=%q lastError=%v err=%v response=%s", afterExplore.Status, afterExplore.LastError, err, exploreRes.Body.String())
	}
	failedConfirmatory, err := store.AppendStudyExecution(ctx, winnerID, "exploratory_complete",
		[]byte(`{"id":"failed-confirm","kind":"confirmatory","status":"failed","outcomes":[]}`), &failedMessage)
	if err != nil || failedConfirmatory.Status != "exploratory_complete" || executionCount(t, failedConfirmatory.Executions) != 3 {
		t.Fatalf("failed confirmatory execution advanced lifecycle: status=%q err=%v", failedConfirmatory.Status, err)
	}
	validate := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/research-studies/"+winnerID+"/validate-holdout", nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	firstValidation := validate()
	if firstValidation.Code != http.StatusOK {
		t.Fatalf("holdout validation returned HTTP %d: %s", firstValidation.Code, firstValidation.Body.String())
	}
	var consumed api.ResearchStudy
	if err := json.Unmarshal(firstValidation.Body.Bytes(), &consumed); err != nil || consumed.Status != "holdout_consumed" {
		t.Fatalf("consumed status=%q err=%v", consumed.Status, err)
	}
	secondValidation := validate()
	var repeated api.ResearchStudy
	if secondValidation.Code != http.StatusOK || json.Unmarshal(secondValidation.Body.Bytes(), &repeated) != nil {
		t.Fatalf("repeat validation returned HTTP %d", secondValidation.Code)
	}
	if len(repeated.Executions) != len(consumed.Executions) {
		t.Fatalf("repeated validation added execution: before=%d after=%d", len(consumed.Executions), len(repeated.Executions))
	}
	formerHoldout := postJSON("/api/backtests", api.BacktestRequest{
		StrategyID: "ema-cross", AssetID: "xauusd", Timeframe: "1d",
		StartDate: "2021-03-01", EndDate: "2021-03-31",
		InitialCapital: 10000, PositionSize: 5, Commission: 0, Slippage: 0, Parameters: map[string]any{},
	})
	if formerHoldout.Code != http.StatusOK && formerHoldout.Code != http.StatusCreated {
		t.Fatalf("former holdout backtest returned HTTP %d: %s", formerHoldout.Code, formerHoldout.Body.String())
	}
	runs, err := svc.ListRuns(ctx, "")
	if err != nil {
		t.Fatalf("list old runs: %v", err)
	}
	comparisons, err := svc.ListComparisons(ctx)
	if err != nil {
		t.Fatalf("list old comparisons: %v", err)
	}
	research, err := svc.ListSignalResearch(ctx)
	if err != nil {
		t.Fatalf("list old signal research: %v", err)
	}
	if len(runs) < len(oldRuns) || len(comparisons) < len(oldComparisons) || len(research) < len(oldResearch) {
		t.Fatalf("existing records disappeared: runs %d→%d, comparisons %d→%d, signal research %d→%d",
			len(oldRuns), len(runs), len(oldComparisons), len(comparisons), len(oldResearch), len(research))
	}
}
