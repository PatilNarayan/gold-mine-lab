package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gold-mine-lab/backend/internal/market"
)

type fakeLive struct {
	status  LiveStatusResponse
	quote   LiveQuoteResponse
	qErr    error
	candles LiveCandlesResponse
	cErr    error
}

func (f fakeLive) LiveStatus(context.Context) LiveStatusResponse { return f.status }
func (f fakeLive) LiveQuote(_ context.Context, asset string) (LiveQuoteResponse, error) {
	return f.quote, f.qErr
}
func (f fakeLive) LiveCandles(_ context.Context, _ string, _ market.Timeframe, _ int) (LiveCandlesResponse, error) {
	return f.candles, f.cErr
}

func liveRouter(svc LiveMarketService) *gin.Engine {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRouter(Deps{
		Logger: log, Health: fakeChecker{}, Live: svc,
		AllowedOrigins: []string{"http://localhost:5173"}, Env: "test",
	})
}

func TestLiveStatusEndpoint(t *testing.T) {
	r := liveRouter(fakeLive{status: LiveStatusResponse{
		Enabled: true, State: "connected", Provider: "twelvedata", Symbol: "XAU/USD",
	}})
	w := doRequest(t, r, http.MethodGet, "/api/market/live/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body LiveStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.State != "connected" || body.Provider != "twelvedata" {
		t.Fatalf("unexpected %+v", body)
	}
}

func TestLiveQuoteValidationErrorIs400(t *testing.T) {
	r := liveRouter(fakeLive{qErr: &ValidationError{Msg: `unsupported asset "tsla"`}})
	w := doRequest(t, r, http.MethodGet, "/api/market/live/quote?asset=tsla", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var env errorEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error.Code != "invalid_request" {
		t.Fatalf("code = %q", env.Error.Code)
	}
}

func TestLiveQuoteUnavailableIs503(t *testing.T) {
	r := liveRouter(fakeLive{qErr: fmt.Errorf("%w: disabled", ErrUnavailable)})
	w := doRequest(t, r, http.MethodGet, "/api/market/live/quote?asset=xauusd", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var env errorEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error.Code != "unavailable" {
		t.Fatalf("code = %q", env.Error.Code)
	}
}

func TestLiveCandlesRequiresParams(t *testing.T) {
	r := liveRouter(fakeLive{})
	for _, path := range []string{
		"/api/market/live/candles",
		"/api/market/live/candles?asset=xauusd",
		"/api/market/live/candles?timeframe=1h",
	} {
		w := doRequest(t, r, http.MethodGet, path, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", path, w.Code)
		}
	}
}

func TestLiveCandlesOK(t *testing.T) {
	vol := 12.5
	r := liveRouter(fakeLive{candles: LiveCandlesResponse{
		AssetID: "xauusd", Provider: "twelvedata", Symbol: "XAU/USD",
		Timeframe: "15m", Status: "connected",
		Candles: []LiveCandle{{Time: "2025-10-17T10:00:00Z", Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: &vol}},
	}})
	w := doRequest(t, r, http.MethodGet, "/api/market/live/candles?asset=xauusd&timeframe=15m&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body LiveCandlesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Candles) != 1 || body.Candles[0].Volume == nil {
		t.Fatalf("unexpected %+v", body)
	}
}
