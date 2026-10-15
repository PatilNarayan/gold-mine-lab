package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

type fakeAssetsRepo struct{ a market.Asset }

func (f fakeAssetsRepo) UpsertAsset(context.Context, market.Asset) error { return nil }
func (f fakeAssetsRepo) ListAssets(context.Context) ([]market.Asset, error) {
	return []market.Asset{f.a}, nil
}
func (f fakeAssetsRepo) GetAsset(_ context.Context, id string) (market.Asset, error) {
	if id == f.a.ID {
		return f.a, nil
	}
	return market.Asset{}, repository.ErrNotFound
}

type srSetCall struct{ id, status string }

type fakeSRRepo struct {
	created int
	sets    []srSetCall
	row     repository.SignalResearchRow
}

func (f *fakeSRRepo) CreateSignalResearch(context.Context, []byte) (string, error) {
	f.created++
	return "sr-1", nil
}
func (f *fakeSRRepo) SetSignalResearchResult(_ context.Context, id, status string, _ []byte, _ *string) error {
	f.sets = append(f.sets, srSetCall{id, status})
	return nil
}
func (f *fakeSRRepo) GetSignalResearch(context.Context, string) (repository.SignalResearchRow, error) {
	return f.row, nil
}
func (f *fakeSRRepo) ListSignalResearch(context.Context) ([]repository.SignalResearchRow, error) {
	return []repository.SignalResearchRow{f.row}, nil
}

func testSRService(t *testing.T, dir string, sr *fakeSRRepo) *Service {
	t.Helper()
	reg := strategy.NewRegistry()
	for _, st := range []strategy.Strategy{strategy.NewEMACross(), strategy.NewRSIMeanReversion(), strategy.NewDonchianBreakout()} {
		if err := reg.Register(st); err != nil {
			t.Fatal(err)
		}
	}
	repos := Repositories{
		Assets:         fakeAssetsRepo{market.Asset{ID: "xauusd", Symbol: "XAUUSD", QuoteCurrency: "USD"}},
		SignalResearch: sr,
	}
	return New(repos, market.NewCSVProvider(dir, nil), reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func writeSRData(t *testing.T, dir string, n int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("Date;Open;High;Low;Close;Volume\n")
	for i := 0; i < n; i++ {
		p := 100 + float64(i%7)
		b.WriteString(fmt.Sprintf("2020.01.%02d 00:00;%.1f;%.1f;%.1f;%.1f;100\n", 1+i, p, p+1, p-1, p+0.5))
	}
	if err := os.WriteFile(filepath.Join(dir, "XAU_1d_data.csv"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func srRequest() api.SignalResearchRequest {
	return api.SignalResearchRequest{
		Legs: []api.SignalResearchLeg{
			{StrategyID: "ema-cross"}, {StrategyID: "rsi-mean-reversion"},
		},
		AssetID: "xauusd", Timeframe: "1d",
		StartDate: "2020-01-01", EndDate: "2020-01-20", EventStudy: true,
	}
}

func TestSignalResearchCancelMarksFailed(t *testing.T) {
	dir := t.TempDir()
	writeSRData(t, dir, 25)
	sr := &fakeSRRepo{}
	svc := testSRService(t, dir, sr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.ExecuteSignalResearch(ctx, srRequest())
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if sr.created != 1 {
		t.Fatalf("expected 1 persisted row, got %d", sr.created)
	}
	if len(sr.sets) != 1 || sr.sets[0].status != "failed" {
		t.Fatalf("expected failed transition after cancel, got %+v", sr.sets)
	}
}

func TestSignalResearchFingerprintFailureBlocksCompletion(t *testing.T) {
	dir := t.TempDir() // no CSV at all → range/fingerprint unavailable
	sr := &fakeSRRepo{}
	svc := testSRService(t, dir, sr)
	_, err := svc.ExecuteSignalResearch(context.Background(), srRequest())
	if err == nil {
		t.Fatal("expected error without dataset")
	}
	if sr.created != 0 {
		t.Fatalf("no row should persist when the dataset/provenance is unavailable, got %d", sr.created)
	}
	for _, c := range sr.sets {
		if c.status == "completed" {
			t.Fatal("a completed status was persisted without provenance")
		}
	}
}

func TestSignalResearchUnknownCandidateNoPanic(t *testing.T) {
	dir := t.TempDir()
	writeSRData(t, dir, 25)
	sr := &fakeSRRepo{}
	svc := testSRService(t, dir, sr)
	req := srRequest()
	req.Legs[1].StrategyID = "nope"
	_, err := svc.ExecuteSignalResearch(context.Background(), req)
	var ve *api.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError for unknown candidate, got %v", err)
	}
}

func TestSignalResearchFromRowIntegrity(t *testing.T) {
	now := time.Now().UTC()
	base := repository.SignalResearchRow{ID: "r1", Status: "completed", CreatedAt: now}

	// Malformed stored result → integrity error, not a zero-valued response.
	bad := base
	bad.Result = []byte("{not-json")
	if _, err := signalResearchFromRow(bad, false); err == nil {
		t.Fatal("malformed result JSON must be an integrity error")
	}

	// Completed record without a stored result → integrity error.
	empty := base
	if _, err := signalResearchFromRow(empty, false); err == nil {
		t.Fatal("completed record without result must be an integrity error")
	}

	// Malformed stored request on a failed row → integrity error.
	failedRow := repository.SignalResearchRow{ID: "r2", Status: "failed", Request: []byte("{oops"), CreatedAt: now}
	if _, err := signalResearchFromRow(failedRow, false); err == nil {
		t.Fatal("malformed request JSON must be an integrity error")
	}

	// Valid completed row round-trips.
	ok := base
	ok.Request = []byte(`{"legs":[{"strategyId":"a"},{"strategyId":"b"}],"assetId":"xauusd","timeframe":"1d","startDate":"2020-01-01","endDate":"2020-02-01"}`)
	ok.Result = []byte(`{"id":"r1","status":"completed","assetId":"xauusd","timeframe":"1d","startDate":"2020-01-01","endDate":"2020-02-01","evaluatedBars":20,"warmupBars":10,"strategyIds":["a","b"],"candidates":[{"strategyId":"a","version":"1","evaluatedBars":20}]}`)
	out, err := signalResearchFromRow(ok, false)
	if err != nil {
		t.Fatalf("valid row: %v", err)
	}
	if out.Status != "completed" || len(out.StrategyIDs) != 2 || len(out.Candidates) != 1 {
		t.Fatalf("valid row decoded wrong: %+v", out)
	}
	list, err := signalResearchFromRow(ok, true)
	if err != nil || list.Candidates != nil {
		t.Fatalf("summary view must null detail fields: %+v", list)
	}

	// Failed row falls back to request metadata for the summary.
	failedOK := repository.SignalResearchRow{ID: "r3", Status: "failed", Request: ok.Request, CreatedAt: now}
	out, err = signalResearchFromRow(failedOK, true)
	if err != nil {
		t.Fatalf("failed row with valid request: %v", err)
	}
	if out.AssetID != "xauusd" || len(out.StrategyIDs) != 2 {
		t.Fatalf("request-derived summary wrong: %+v", out)
	}
}
