package config

import (
	"strings"
	"testing"
	"time"
)

// setValidEnv populates every variable with a valid value.
func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("DB_MAX_CONNS", "20")
	t.Setenv("DB_MIN_CONNS", "4")
	t.Setenv("HTTP_READ_TIMEOUT", "5s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "7s")
	t.Setenv("SHUTDOWN_TIMEOUT", "20s")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:5173, https://app.example.com")
}

func TestLoadValidEnv(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Env != "test" {
		t.Errorf("Env = %q, want test", cfg.Env)
	}
	if cfg.HTTP.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.HTTP.Port)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if cfg.DB.URL != "postgres://u:p@localhost:5432/db" {
		t.Errorf("DB.URL = %q", cfg.DB.URL)
	}
	if cfg.DB.MaxConns != 20 || cfg.DB.MinConns != 4 {
		t.Errorf("pool conns = %d/%d, want 20/4", cfg.DB.MaxConns, cfg.DB.MinConns)
	}
	if cfg.HTTP.ReadTimeout != 5*time.Second || cfg.HTTP.ShutdownTimeout != 20*time.Second {
		t.Errorf("timeouts = %v/%v", cfg.HTTP.ReadTimeout, cfg.HTTP.ShutdownTimeout)
	}
	if len(cfg.HTTP.AllowedOrigins) != 2 || cfg.HTTP.AllowedOrigins[1] != "https://app.example.com" {
		t.Errorf("AllowedOrigins = %v", cfg.HTTP.AllowedOrigins)
	}
}

func TestLoadMissingDatabaseURL(t *testing.T) {
	setValidEnv(t)
	t.Setenv("DATABASE_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded with empty DATABASE_URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error does not name the missing var: %v", err)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	setValidEnv(t)
	t.Setenv("APP_ENV", "staging-ish")
	t.Setenv("HTTP_PORT", "not-a-number")
	t.Setenv("LOG_LEVEL", "verbose")
	t.Setenv("HTTP_READ_TIMEOUT", "soon")
	t.Setenv("DB_MIN_CONNS", "50") // exceeds DB_MAX_CONNS=20
	t.Setenv("CORS_ALLOWED_ORIGINS", "not-a-url")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded with invalid env")
	}
	msg := err.Error()
	for _, want := range []string{"APP_ENV", "HTTP_PORT", "LOG_LEVEL", "HTTP_READ_TIMEOUT", "DB_MIN_CONNS", "CORS_ALLOWED_ORIGINS"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error listing missing %q:\n%s", want, msg)
		}
	}
}

func TestLoadTiingoProviderSelection(t *testing.T) {
	setValidEnv(t)
	t.Setenv("LIVE_MARKET_ENABLED", "true")
	t.Setenv("LIVE_MARKET_PROVIDER", "tiingo")
	t.Setenv("TIINGO_API_TOKEN", "x")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Live.Provider != "tiingo" || cfg.Live.TiingoToken != "x" {
		t.Fatalf("tiingo config not loaded: %+v", cfg.Live)
	}
	// websocket transport is implied — no explicit setting needed
	if cfg.Live.Transport != "websocket" {
		t.Fatalf("transport = %q, want websocket default for tiingo", cfg.Live.Transport)
	}
}

func TestLoadTiingoRejectsRestTransport(t *testing.T) {
	setValidEnv(t)
	t.Setenv("LIVE_MARKET_PROVIDER", "tiingo")
	t.Setenv("LIVE_MARKET_TRANSPORT", "rest")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "tiingo") {
		t.Fatalf("expected tiingo transport rejection, got %v", err)
	}
}

func TestLoadRejectsUnknownProvider(t *testing.T) {
	setValidEnv(t)
	t.Setenv("LIVE_MARKET_PROVIDER", "polygon")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "LIVE_MARKET_PROVIDER") {
		t.Fatalf("expected provider rejection, got %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	// Only the required var set; everything else falls back to defaults.
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.HTTP.Port != 8080 || cfg.Log.Level != "info" || cfg.Env != "dev" {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if len(cfg.HTTP.AllowedOrigins) != 1 {
		t.Errorf("default origins = %v", cfg.HTTP.AllowedOrigins)
	}
}
