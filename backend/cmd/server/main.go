// Gold Mine Lab backend entrypoint: thin wiring only.
// config → logger → pool → router → serve → graceful shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/config"
	"github.com/gold-mine-lab/backend/internal/database"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository/postgres"
	"github.com/gold-mine-lab/backend/internal/service"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/migrations"
)

func main() {
	// shutdown carries SIGINT/SIGTERM into run(). It's a parameter so tests can
	// inject a signal without a real console event.
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	if err := run(shutdown); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

func run(shutdown <-chan os.Signal) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)
	log.Info("starting", "env", cfg.Env, "port", cfg.HTTP.Port)

	ctx := context.Background()

	// Apply pending schema migrations before building the pool's dependents.
	if err := migrations.Up(cfg.DB.URL); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	log.Info("migrations applied")

	pool, err := database.NewPool(ctx, cfg.DB)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	// Real pipeline: CSV provider → registry → service → repositories.
	if cfg.MarketDataDir == "" {
		return fmt.Errorf("MARKET_DATA_DIR is required (historical CSV directory)")
	}
	data := market.NewCSVProvider(cfg.MarketDataDir, nil)
	log.Info("market data directory configured", "dir", cfg.MarketDataDir)

	reg := strategy.NewRegistry()
	if err := reg.Register(strategy.NewEMACross()); err != nil {
		return fmt.Errorf("register strategy: %w", err)
	}

	store := postgres.New(pool.Pool)
	svc := service.New(service.Repositories{
		Assets: store, Strategies: store, Signals: store,
		Runs: store, Trades: store, Metrics: store,
	}, data, reg, log)

	// Seed asset/strategies, generate real signals, run seed backtests on an
	// empty database — all idempotent.
	if err := svc.Bootstrap(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("bootstrap: %w", err)
	}
	router := api.NewRouter(api.Deps{
		Logger:         log,
		Health:         pool,
		Market:         svc,
		Strategies:     svc,
		Backtests:      svc,
		Trades:         svc,
		AllowedOrigins: cfg.HTTP.AllowedOrigins,
		Env:            cfg.Env,
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTP.Port),
		Handler:      router,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}

	// Serve until SIGINT/SIGTERM.
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-shutdown:
	}

	log.Info("shutdown signal received, draining requests",
		"timeout", cfg.HTTP.ShutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(ctx, cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		pool.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	pool.Close()
	log.Info("stopped cleanly")
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.Log.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Env == "prod" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
