// Package config loads and validates runtime configuration from environment
// variables. Validation fails fast and reports every missing or invalid
// variable so operators get a complete picture on the first run.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the application's typed runtime configuration.
type Config struct {
	Env           string // APP_ENV: "dev" | "prod"
	MarketDataDir string // MARKET_DATA_DIR: directory of historical CSVs (optional)
	HTTP          HTTPConfig
	Log           LogConfig
	DB            DBConfig
	Live          LiveConfig
}

// HTTPConfig holds HTTP server settings.
type HTTPConfig struct {
	Port            int           // HTTP_PORT
	ReadTimeout     time.Duration // HTTP_READ_TIMEOUT, e.g. "10s"
	WriteTimeout    time.Duration // HTTP_WRITE_TIMEOUT, e.g. "10s"
	ShutdownTimeout time.Duration // SHUTDOWN_TIMEOUT, e.g. "15s"
	AllowedOrigins  []string      // CORS_ALLOWED_ORIGINS, comma-separated
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level string // LOG_LEVEL: "debug" | "info" | "warn" | "error"
}

// DBConfig holds PostgreSQL pool settings.
type DBConfig struct {
	URL      string // DATABASE_URL — required, no default (contains credentials)
	MaxConns int32  // DB_MAX_CONNS
	MinConns int32  // DB_MIN_CONNS
}

// LiveConfig holds the optional live market-data provider settings. Live data
// is observational only — it never feeds backtests or persisted research.
type LiveConfig struct {
	Enabled       bool          // LIVE_MARKET_ENABLED
	Provider      string        // LIVE_MARKET_PROVIDER: only "twelvedata" supported
	APIKey        string        // TWELVE_DATA_API_KEY — secret, never logged or served
	Symbol        string        // LIVE_MARKET_SYMBOL — provider symbol, default XAU/USD
	StaleAfter    time.Duration // LIVE_MARKET_STALE_AFTER — quote age → "stale"
	Transport     string        // LIVE_MARKET_TRANSPORT: "rest" (default) | "websocket"
	PollInterval  time.Duration // LIVE_MARKET_POLL_INTERVAL — REST quote cadence
	CandleRefresh time.Duration // LIVE_MARKET_CANDLE_RECONCILE_INTERVAL — per-timeframe REST refresh
	CreditLimit   int           // LIVE_MARKET_DAILY_CREDIT_LIMIT — plan allowance for usage warnings
	RESTBaseURL   string        // TWELVE_DATA_REST_URL — override for tests/mocks
	WSURL         string        // TWELVE_DATA_WS_URL — override for tests/mocks
}

// Load reads configuration from the environment, applies safe defaults for
// non-secret values, and validates the result. Every problem found is listed
// in the returned error.
func Load() (Config, error) {
	var errs []string
	var cfg Config

	cfg.MarketDataDir = os.Getenv("MARKET_DATA_DIR")

	cfg.Env = envOr("APP_ENV", "dev")
	if cfg.Env != "dev" && cfg.Env != "prod" && cfg.Env != "test" {
		errs = append(errs, fmt.Sprintf("APP_ENV must be dev|prod|test, got %q", cfg.Env))
	}

	cfg.HTTP.Port = envInt("HTTP_PORT", 8080, &errs)
	if cfg.HTTP.Port < 1 || cfg.HTTP.Port > 65535 {
		errs = append(errs, fmt.Sprintf("HTTP_PORT out of range: %d", cfg.HTTP.Port))
	}

	cfg.Log.Level = strings.ToLower(envOr("LOG_LEVEL", "info"))
	switch cfg.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Sprintf("LOG_LEVEL must be debug|info|warn|error, got %q", cfg.Log.Level))
	}

	cfg.DB.URL = os.Getenv("DATABASE_URL")
	if cfg.DB.URL == "" {
		errs = append(errs, "DATABASE_URL is required (no default — it contains credentials)")
	}

	cfg.DB.MaxConns = int32(envInt("DB_MAX_CONNS", 10, &errs))
	cfg.DB.MinConns = int32(envInt("DB_MIN_CONNS", 2, &errs))
	if cfg.DB.MinConns < 0 {
		errs = append(errs, "DB_MIN_CONNS cannot be negative")
	}
	if cfg.DB.MaxConns < 1 {
		errs = append(errs, "DB_MAX_CONNS must be >= 1")
	}
	if cfg.DB.MaxConns >= 1 && cfg.DB.MinConns > cfg.DB.MaxConns {
		errs = append(errs, "DB_MIN_CONNS cannot exceed DB_MAX_CONNS")
	}

	cfg.HTTP.ReadTimeout = envDuration("HTTP_READ_TIMEOUT", 10*time.Second, &errs)
	cfg.HTTP.WriteTimeout = envDuration("HTTP_WRITE_TIMEOUT", 10*time.Second, &errs)
	cfg.HTTP.ShutdownTimeout = envDuration("SHUTDOWN_TIMEOUT", 15*time.Second, &errs)

	origins := envOr("CORS_ALLOWED_ORIGINS", "http://localhost:5173")
	for _, o := range strings.Split(origins, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
			errs = append(errs, fmt.Sprintf("CORS_ALLOWED_ORIGINS entry %q must start with http(s)://", o))
			continue
		}
		cfg.HTTP.AllowedOrigins = append(cfg.HTTP.AllowedOrigins, o)
	}
	if len(cfg.HTTP.AllowedOrigins) == 0 && len(errs) == 0 {
		errs = append(errs, "CORS_ALLOWED_ORIGINS must list at least one origin")
	}

	cfg.Live.Enabled = envBool("LIVE_MARKET_ENABLED", false, &errs)
	cfg.Live.Provider = envOr("LIVE_MARKET_PROVIDER", "twelvedata")
	cfg.Live.APIKey = os.Getenv("TWELVE_DATA_API_KEY")
	cfg.Live.Symbol = envOr("LIVE_MARKET_SYMBOL", "XAU/USD")
	cfg.Live.StaleAfter = envDuration("LIVE_MARKET_STALE_AFTER", 30*time.Second, &errs)
	cfg.Live.Transport = envOr("LIVE_MARKET_TRANSPORT", "rest")
	cfg.Live.PollInterval = envDuration("LIVE_MARKET_POLL_INTERVAL", 10*time.Second, &errs)
	cfg.Live.CandleRefresh = envDuration("LIVE_MARKET_CANDLE_RECONCILE_INTERVAL", 5*time.Minute, &errs)
	cfg.Live.CreditLimit = envInt("LIVE_MARKET_DAILY_CREDIT_LIMIT", 800, &errs)
	cfg.Live.RESTBaseURL = envOr("TWELVE_DATA_REST_URL", "https://api.twelvedata.com")
	cfg.Live.WSURL = envOr("TWELVE_DATA_WS_URL", "wss://ws.twelvedata.com/v1/quotes/price")
	if cfg.Live.Provider != "twelvedata" {
		errs = append(errs, fmt.Sprintf("LIVE_MARKET_PROVIDER must be twelvedata, got %q", cfg.Live.Provider))
	}
	switch cfg.Live.Transport {
	case "rest", "websocket":
	default:
		errs = append(errs, fmt.Sprintf("LIVE_MARKET_TRANSPORT must be rest|websocket, got %q", cfg.Live.Transport))
	}
	if cfg.Live.PollInterval < 5*time.Second || cfg.Live.PollInterval > 10*time.Minute {
		errs = append(errs, "LIVE_MARKET_POLL_INTERVAL must be between 5s and 10m")
	}
	if cfg.Live.CandleRefresh < 30*time.Second || cfg.Live.CandleRefresh > time.Hour {
		errs = append(errs, "LIVE_MARKET_CANDLE_RECONCILE_INTERVAL must be between 30s and 1h")
	}
	if cfg.Live.CreditLimit <= 0 {
		errs = append(errs, "LIVE_MARKET_DAILY_CREDIT_LIMIT must be > 0")
	}
	if cfg.Live.StaleAfter < time.Second {
		errs = append(errs, "LIVE_MARKET_STALE_AFTER must be >= 1s")
	}
	// A missing TWELVE_DATA_API_KEY is NOT a config error: the provider parks
	// in the config_error state at runtime and history keeps working.

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool, errs *[]string) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s must be a boolean (true|false), got %q", key, v))
		return def
	}
	return b
}

func envInt(key string, def int, errs *[]string) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s must be an integer, got %q", key, v))
		return def
	}
	return n
}

func envDuration(key string, def time.Duration, errs *[]string) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s must be a duration like \"10s\" or \"500ms\", got %q", key, v))
		return def
	}
	return d
}
