// Package api wires the HTTP surface: middleware, routes, and the consistent
// JSON error envelope. Routers take their dependencies explicitly — there are
// no globals, so tests can build isolated instances.
package api

import (
	"log/slog"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Deps are the router's dependencies, injected by the caller.
type Deps struct {
	Logger         *slog.Logger
	Health         HealthChecker
	Market         MarketService
	Strategies     StrategyService
	Backtests      BacktestService
	Trades         TradeService
	Comparisons    ComparisonService
	WalkForwards   WalkForwardService
	StrategyGraphs StrategyGraphService
	SignalResearch SignalResearchService
	Studies        ResearchStudyService
	Live           LiveMarketService
	AllowedOrigins []string
	// Env is "dev" | "prod" | "test" — controls gin's mode.
	Env string
}

// NewRouter builds a gin engine with all middleware and routes registered.
func NewRouter(d Deps) *gin.Engine {
	switch d.Env {
	case "prod":
		gin.SetMode(gin.ReleaseMode)
	case "test":
		gin.SetMode(gin.TestMode)
	}

	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.Use(requestID())
	r.Use(requestLogger(d.Logger))
	r.Use(recovery(d.Logger))
	r.Use(cors.New(cors.Config{
		AllowOrigins:     d.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", RequestIDHeader},
		ExposeHeaders:    []string{RequestIDHeader},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	r.NoRoute(notFoundHandler())
	r.NoMethod(methodNotAllowedHandler())

	v1 := r.Group("/api")
	v1.GET("/health", healthHandler(d.Health))
	if d.Market != nil {
		registerMarketRoutes(v1, d.Market)
	}
	if d.Strategies != nil {
		registerStrategyRoutes(v1, d.Strategies)
	}
	if d.Backtests != nil {
		registerBacktestRoutes(v1, d.Backtests)
	}
	if d.Trades != nil {
		registerTradeRoutes(v1, d.Trades)
	}
	if d.Comparisons != nil {
		registerComparisonRoutes(v1, d.Comparisons)
	}
	if d.WalkForwards != nil {
		registerWalkForwardRoutes(v1, d.WalkForwards)
	}
	if d.StrategyGraphs != nil {
		registerStrategyGraphRoutes(v1, d.StrategyGraphs)
	}
	if d.SignalResearch != nil {
		registerSignalResearchRoutes(v1, d.SignalResearch)
	}
	if d.Studies != nil {
		registerResearchStudyRoutes(v1, d.Studies)
	}
	if d.Live != nil {
		registerLiveMarketRoutes(v1, d.Live)
	}

	return r
}
