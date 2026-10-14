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
	Env  string // APP_ENV: "dev" | "prod"
	HTTP HTTPConfig
	Log  LogConfig
	DB   DBConfig
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

// Load reads configuration from the environment, applies safe defaults for
// non-secret values, and validates the result. Every problem found is listed
// in the returned error.
func Load() (Config, error) {
	var errs []string
	var cfg Config

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
