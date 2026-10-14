// Package tests — API surface smoke tests against the in-memory demo service.
// No external infrastructure required.
package tests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/demo"
)

type upHealth struct{}

func (upHealth) Ping(context.Context) error { return nil }

func testRouter() *gin.Engine {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := demo.New()
	return api.NewRouter(api.Deps{
		Logger:         log,
		Health:         upHealth{},
		Market:         svc,
		Strategies:     svc,
		Backtests:      svc,
		Trades:         svc,
		AllowedOrigins: []string{"http://localhost:5173"},
		Env:            "test",
	})
}

func get(t *testing.T, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	testRouter().ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func getArr(t *testing.T, path string) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	testRouter().ServeHTTP(w, req)
	var body []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func TestAssetsEndpoint(t *testing.T) {
	w, arr := getArr(t, "/api/assets")
	if w.Code != http.StatusOK || len(arr) == 0 {
		t.Fatalf("status=%d len=%d", w.Code, len(arr))
	}
	if arr[0]["symbol"] == "" || arr[0]["assetClass"] == "" {
		t.Errorf("asset shape wrong: %v", arr[0])
	}
}

func TestCandlesEndpoint(t *testing.T) {
	w, body := get(t, "/api/market/candles?asset=xauusd&timeframe=1h")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%v", w.Code, body)
	}
	candles, ok := body["candles"].([]any)
	if !ok || len(candles) == 0 {
		t.Fatal("no candles")
	}
	overlays := body["overlays"].(map[string]any)
	if len(overlays["ema20"].([]any)) == 0 || len(overlays["ema50"].([]any)) == 0 {
		t.Error("overlays missing")
	}
}

func TestBacktestEndpoints(t *testing.T) {
	// latest
	w, body := get(t, "/api/backtests/latest")
	if w.Code != http.StatusOK || body["runId"] == nil {
		t.Fatalf("latest: status=%d body=%v", w.Code, body)
	}
	runID := body["runId"].(string)

	// detail
	w, body = get(t, "/api/backtests/"+runID)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status=%d", w.Code)
	}
	if body["metrics"] == nil || len(body["equity"].([]any)) == 0 || len(body["trades"].([]any)) == 0 {
		t.Error("detail missing metrics/equity/trades")
	}

	// unknown run → 404 envelope
	w, body = get(t, "/api/backtests/run-999")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown run status=%d", w.Code)
	}
	if body["error"].(map[string]any)["code"] != "not_found" {
		t.Error("envelope missing not_found code")
	}

	// summary
	w, body = get(t, "/api/backtests/summary")
	if w.Code != http.StatusOK || body["totalBacktests"].(float64) == 0 {
		t.Fatalf("summary: %v", body)
	}
}

func TestTradesFilters(t *testing.T) {
	_, body := get(t, "/api/trades?page=1&pageSize=5")
	items := body["items"].([]any)
	if len(items) != 5 || int(body["pageSize"].(float64)) != 5 {
		t.Fatalf("pagination wrong: %v", body)
	}
	totalAll := body["total"].(float64)

	_, body = get(t, "/api/trades?side=BUY")
	totalBuy := body["total"].(float64)
	if totalBuy == 0 || totalBuy >= totalAll {
		t.Errorf("side filter ineffective: all=%v buy=%v", totalAll, totalBuy)
	}
}

func TestPostBacktest(t *testing.T) {
	r := testRouter()
	payload := `{"assetId":"xauusd","strategyId":"ema-cross","timeframe":"1h","startDate":"2024-03-01","endDate":"2024-10-01","initialCapital":50000,"positionSize":5,"commission":0.02,"slippage":0.05,"parameters":{"fastLength":20}}`
	req := httptest.NewRequest(http.MethodPost, "/api/backtests", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "completed" || body["id"] == nil {
		t.Errorf("unexpected run: %v", body)
	}

	// invalid: end before start → 400
	payload = `{"assetId":"xauusd","strategyId":"ema-cross","timeframe":"1h","startDate":"2024-10-01","endDate":"2024-03-01","initialCapital":50000,"positionSize":5,"commission":0,"slippage":0,"parameters":{}}`
	req = httptest.NewRequest(http.MethodPost, "/api/backtests", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid POST status=%d", w.Code)
	}
}
