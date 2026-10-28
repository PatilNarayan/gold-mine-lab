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
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/config"
	"github.com/gold-mine-lab/backend/internal/database"
	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository/postgres"
	"github.com/gold-mine-lab/backend/internal/service"
	"github.com/gold-mine-lab/backend/internal/shadow"
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

	// runCtx carries the app lifetime to every background worker — it is
	// cancelled exactly once, inside the ordered shutdown below.
	runCtx, cancelRun := context.WithCancel(ctx)

	// Ordered shutdown — the SINGLE deterministic owner. Workers register
	// stop hooks via these vars; this defer runs them in the documented
	// order and the DB pool always closes LAST. Provider Close hooks are
	// captured here (not `defer p.Close()`, which would run first under
	// Go's LIFO defer order and invert the sequence).
	var shadowRt *shadow.Runtime
	var driverDone chan struct{}
	var stopRecorder func()
	var closeProvider func() error
	defer func() {
		orderedShutdown(log, cfg.HTTP.ShutdownTimeout, cancelRun, driverDone,
			shadowRt, stopRecorder, closeProvider, pool.Close)
	}()

	// Real pipeline: provider-synced Postgres candle store → registry →
	// service → repositories. Historical candles come exclusively from the
	// configured market provider — no bundled CSV datasets at runtime.
	reg := strategy.NewRegistry()
	if err := reg.Register(strategy.NewEMACross()); err != nil {
		return err
	}
	if err := reg.Register(strategy.NewRSIMeanReversion()); err != nil {
		return fmt.Errorf("register strategy: %w", err)
	}
	if err := reg.Register(strategy.NewDonchianBreakout()); err != nil {
		return fmt.Errorf("register strategy: %w", err)
	}

	store := postgres.New(pool.Pool)

	// Live + historical market data: one managed upstream provider feeding
	// the realtime quote/candle caches AND serving as the historical source
	// for the persistent candle store. Same provider on both sides keeps the
	// pricing convention coherent — no silent cross-provider mixing.
	var liveProvider live.Provider
	if cfg.Live.Enabled {
		switch cfg.Live.Provider {
		case "tiingo":
			p := live.NewTiingo(live.TiingoConfig{
				APIToken:      cfg.Live.TiingoToken,
				WSURL:         cfg.Live.TiingoWSURL,
				RESTBaseURL:   cfg.Live.TiingoRESTURL,
				Ticker:        "xauusd",
				Symbol:        cfg.Live.Symbol,
				AssetID:       market.CanonicalAssetID,
				StaleAfter:    cfg.Live.StaleAfter,
				CandleRefresh: cfg.Live.CandleRefresh,
				Logger:        log,
			})
			p.Start(ctx)
			closeProvider = p.Close
			liveProvider = p
		default:
			p := live.NewTwelveData(live.TwelveDataConfig{
				APIKey:        cfg.Live.APIKey,
				Transport:     cfg.Live.Transport,
				PollInterval:  cfg.Live.PollInterval,
				RESTBaseURL:   cfg.Live.RESTBaseURL,
				WSURL:         cfg.Live.WSURL,
				Symbol:        cfg.Live.Symbol,
				AssetID:       market.CanonicalAssetID,
				StaleAfter:    cfg.Live.StaleAfter,
				CandleRefresh: cfg.Live.CandleRefresh,
				Logger:        log,
			})
			p.Start(ctx)
			closeProvider = p.Close
			liveProvider = p
		}
		st := liveProvider.Status()
		est := st.EstimatedDailyCredits
		// Log the provider's ACTUAL transport/symbol — cfg.Live.Transport is a
		// Twelve Data-only knob and lies about Tiingo (websocket-only).
		log.Info("live market data enabled", "provider", st.Provider,
			"transport", st.Transport, "pollInterval", cfg.Live.PollInterval,
			"candleRefresh", cfg.Live.CandleRefresh, "estDailyCredits", est,
			"symbol", st.Symbol)
		if est > cfg.Live.CreditLimit {
			log.Warn("estimated daily API credits exceed plan allowance — raise intervals or the plan",
				"estDailyCredits", est, "dailyLimit", cfg.Live.CreditLimit,
				"hint", "increase LIVE_MARKET_POLL_INTERVAL / LIVE_MARKET_CANDLE_RECONCILE_INTERVAL")
		}
	}

	// Canonical historical source: Postgres candle store backfilled by the
	// configured provider on demand. Read-only (no syncs) when live is off.
	var histFn market.HistoryFunc
	var histFloor time.Time
	if liveProvider != nil {
		p := liveProvider
		histFloor = p.HistoryFloor()
		histFn = func(ctx context.Context, tf market.Timeframe, from, to time.Time,
			onPage func([]market.Candle, time.Time, time.Time) error) error {
			return p.FetchHistoryPages(ctx, string(tf), from, to,
				func(rows []live.Candle, cf, ct time.Time) error {
					out := make([]market.Candle, len(rows))
					for i, r := range rows {
						out[i] = market.Candle{Time: r.Time, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close}
						if r.Volume != nil {
							out[i].Volume = *r.Volume
						}
					}
					return onPage(out, cf, ct)
				})
		}
	}
	data := market.NewDBProvider(postgres.NewCandleStore(pool.Pool),
		cfg.Live.Provider, histFloor, histFn)

	// Prospective market-cost evidence: record distinct live bid/ask quote
	// events (deduplicated, staleness-flagged) — never fabricated rows.
	if liveProvider != nil {
		quoteRec := service.NewQuoteRecorder(store, store, cfg.Live.Provider, cfg.Live.Symbol, log)
		quoteRec.Start(runCtx, liveProvider)
		// Captured as a closure — never a typed-nil interface: a nil
		// *QuoteRecorder in interface{} is non-nil and Stop() would panic.
		stopRecorder = quoteRec.Stop
		log.Info("quote observation recorder started", "provider", cfg.Live.Provider, "symbol", cfg.Live.Symbol)
	}

	svc := service.New(service.Repositories{
		Assets: store, Strategies: store, Signals: store,
		Runs: store, Trades: store, Metrics: store, Comparisons: store, WalkForwards: store, StrategyGraphs: store, SignalResearch: store,
		Studies: store,
	}, data, reg, log)

	// Seed asset/strategies, generate real signals, run seed backtests on an
	// empty database — all idempotent. Historical sync happens lazily on
	// first read and eagerly for the coarser timeframes inside Bootstrap.
	if err := svc.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	liveSvc := service.NewLiveService(liveProvider)

	// Shadow trading (SIMULATED): benchmark EMA cross as control only — it
	// has no demonstrated edge. A single-writer runtime owns the engine;
	// the driver produces events; fills are hypothetical and persisted for
	// replay/recovery. An advisory lock refuses a second concurrent owner.
	var shadowSvc api.ShadowService
	if liveProvider != nil {
		if strat, ok := reg.Get("ema-cross"); ok {
			shadowCfg := shadow.Config{
				StrategyID: strat.ID(), StrategyVersion: strat.Version(),
				Symbol: cfg.Live.Symbol, Timeframe: market.Timeframe1h,
				Provider:      cfg.Live.Provider,
				InitialEquity: 100000, PositionSizePct: 10,
				CommissionPct: 0.02, // same assumptions as the historical benchmark
			}
			own, err := store.AcquireShadowOwnership(ctx, shadow.OwnershipKey(shadowCfg))
			if err != nil {
				log.Warn("shadow runtime not started — exclusive ownership refused",
					"err", err, "strategy", strat.ID())
			} else {
				eng := shadow.New(shadowCfg, store, log)
				shadowRt = shadow.NewRuntime(eng, shadow.RuntimeOptions{Ownership: own, Logger: log})
				asset := market.Asset{ID: "xauusd", Symbol: cfg.Live.Symbol}
				driver := service.NewShadowDriver(shadowRt, liveProvider, data, asset, strat, shadowCfg, store, log)
				shadowRt.Start(runCtx, driver.Bootstrap)
				driverDone = make(chan struct{})
				go func() { driver.Start(runCtx); close(driverDone) }()
				shadowSvc = service.NewShadowService(shadowRt, store, store, store, shadowCfg)
				log.Info("shadow runtime started (simulated)", "strategy", strat.ID(), "version", strat.Version())
			}
		}
	}

	router := api.NewRouter(api.Deps{
		Logger:         log,
		Health:         pool,
		Market:         svc,
		Strategies:     svc,
		Backtests:      svc,
		Trades:         svc,
		Comparisons:    svc,
		WalkForwards:   svc,
		StrategyGraphs: svc,
		SignalResearch: svc,
		Studies:        svc,
		Live:           liveSvc,
		Shadow:         shadowSvc,
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
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("stopped cleanly")
	return nil
}

// orderedShutdown is the single deterministic shutdown owner:
//  1. cancel producers — nothing new is admitted anywhere
//  2. join the shadow driver so producers stop submitting events
//  3. drain admitted runtime events within drainTimeout — deadline expiry
//     is reported; leftover events are counted dropped, never claimed durable
//  4. stop the quote recorder (it drains its own buffer to the DB)
//  5. close the live provider connection
//  6. close the database pool LAST — no worker may write after this
//
// A blocked/unhealthy runtime cannot hang shutdown: Stop honors drainTimeout.
// Every step is nil-safe so partial startup failures still shut down cleanly.
func orderedShutdown(log *slog.Logger, drainTimeout time.Duration, cancel context.CancelFunc,
	driverDone <-chan struct{}, rt *shadow.Runtime, recStop func(),
	closeProvider func() error, closeDB func()) {
	cancel()
	if driverDone != nil {
		<-driverDone
	}
	if rt != nil {
		drainCtx, c := context.WithTimeout(context.Background(), drainTimeout)
		if err := rt.Stop(drainCtx); err != nil {
			log.Warn("shadow runtime drain incomplete — admitted events left unprocessed", "err", err)
		}
		c()
	}
	if recStop != nil {
		recStop()
	}
	if closeProvider != nil {
		if err := closeProvider(); err != nil {
			log.Warn("provider close failed", "err", err)
		}
	}
	closeDB()
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
