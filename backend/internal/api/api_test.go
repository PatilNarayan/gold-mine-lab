package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeChecker struct{ err error }

func (f fakeChecker) Ping(context.Context) error { return f.err }

func testRouter(hc HealthChecker) *gin.Engine {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRouter(Deps{
		Logger:         log,
		Health:         hc,
		AllowedOrigins: []string{"http://localhost:5173"},
		Env:            "test",
	})
}

func doRequest(t *testing.T, r *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHealthOK(t *testing.T) {
	w := doRequest(t, testRouter(fakeChecker{}), http.MethodGet, "/api/health", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Status != "ok" || body.Service != "gold-mine-lab-backend" || body.Database != "up" {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestHealthDegraded(t *testing.T) {
	w := doRequest(t, testRouter(fakeChecker{err: errors.New("connection refused")}), http.MethodGet, "/api/health", nil)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var body healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Status != "degraded" || body.Database != "down" {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestNotFoundUsesErrorEnvelope(t *testing.T) {
	w := doRequest(t, testRouter(fakeChecker{}), http.MethodGet, "/api/nope", nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var env errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if env.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", env.Error.Code)
	}
	if env.Error.RequestID == "" {
		t.Error("request_id missing from error envelope")
	}
	if w.Header().Get(RequestIDHeader) == "" {
		t.Error("X-Request-ID response header missing")
	}
}

func TestMethodNotAllowedUsesErrorEnvelope(t *testing.T) {
	w := doRequest(t, testRouter(fakeChecker{}), http.MethodDelete, "/api/health", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	var env errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if env.Error.Code != "method_not_allowed" {
		t.Errorf("code = %q, want method_not_allowed", env.Error.Code)
	}
}

func TestRequestIDPassthrough(t *testing.T) {
	w := doRequest(t, testRouter(fakeChecker{}), http.MethodGet, "/api/nope", map[string]string{
		RequestIDHeader: "req-abc-123",
	})
	if got := w.Header().Get(RequestIDHeader); got != "req-abc-123" {
		t.Errorf("X-Request-ID = %q, want passthrough", got)
	}
	var env errorEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error.RequestID != "req-abc-123" {
		t.Errorf("error.request_id = %q, want req-abc-123", env.Error.RequestID)
	}
}

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	w := doRequest(t, testRouter(fakeChecker{}), http.MethodOptions, "/api/health", map[string]string{
		"Origin":                         "http://localhost:5173",
		"Access-Control-Request-Method":  "GET",
		"Access-Control-Request-Headers": "content-type",
	})
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("Allow-Origin = %q", got)
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	w := doRequest(t, testRouter(fakeChecker{}), http.MethodOptions, "/api/health", map[string]string{
		"Origin":                        "https://evil.example.com",
		"Access-Control-Request-Method": "GET",
	})
	if got := w.Header().Get("Access-Control-Allow-Origin"); strings.Contains(got, "evil") {
		t.Errorf("Allow-Origin leaked unknown origin: %q", got)
	}
}

func TestRecoveryReturnsEnvelope(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouter(Deps{Logger: log, Health: fakeChecker{}, AllowedOrigins: []string{"*"}, Env: "test"})
	r.GET("/api/panic", func(c *gin.Context) { panic("boom") })

	w := doRequest(t, r, http.MethodGet, "/api/panic", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var env errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if env.Error.Code != "internal_error" || strings.Contains(env.Error.Message, "boom") {
		t.Errorf("leaked internal detail or wrong code: %+v", env.Error)
	}
}
