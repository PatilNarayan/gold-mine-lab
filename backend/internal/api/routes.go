package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/gold-mine-lab/backend/internal/market"
)

func registerResearchStudyRoutes(g *gin.RouterGroup, svc ResearchStudyService) {
	studies := g.Group("/research-studies")
	studies.POST("", func(c *gin.Context) {
		var req ResearchStudyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid research study request")
			return
		}
		out, err := svc.CreateStudy(c.Request.Context(), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, out)
	})
	studies.GET("", func(c *gin.Context) {
		out, err := svc.ListStudies(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	studies.GET("/:id", func(c *gin.Context) {
		out, err := svc.GetStudy(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	studies.PATCH("/:id", func(c *gin.Context) {
		var req ResearchStudyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid research study request")
			return
		}
		out, err := svc.UpdateStudy(c.Request.Context(), c.Param("id"), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	for _, action := range []struct {
		path string
		run  func(context.Context, string) (ResearchStudy, error)
	}{
		{path: "/:id/freeze", run: svc.FreezeStudy},
		{path: "/:id/exploratory-runs", run: svc.RunStudyExploratory},
		{path: "/:id/validate-holdout", run: svc.ValidateStudyHoldout},
	} {
		action := action
		studies.POST(action.path, func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			if err != nil || len(bytes.TrimSpace(body)) != 0 {
				writeError(c, http.StatusBadRequest, "invalid_request", "this action does not accept a request body")
				return
			}
			out, err := action.run(c.Request.Context(), c.Param("id"))
			if err != nil {
				respondErr(c, err)
				return
			}
			c.JSON(http.StatusOK, out)
		})
	}
}

// Handlers stay thin: parse request, call a service interface, write JSON.
// Errors map to the standard envelope — internals are never leaked.

func respondErr(c *gin.Context, err error) {
	var ve *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(c, http.StatusNotFound, "not_found", "resource not found")
	case errors.As(err, &ve):
		writeError(c, http.StatusBadRequest, "invalid_request", ve.Msg)
	case errors.Is(err, ErrUnavailable):
		writeError(c, http.StatusServiceUnavailable, "unavailable", "live market data is not currently available")
	default:
		writeError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func registerMarketRoutes(g *gin.RouterGroup, svc MarketService) {
	g.GET("/assets", func(c *gin.Context) {
		out, err := svc.Assets(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/market/candles", func(c *gin.Context) {
		asset := c.Query("asset")
		tf := c.Query("timeframe")
		if asset == "" || tf == "" {
			writeError(c, http.StatusBadRequest, "invalid_request", "asset and timeframe query params are required")
			return
		}
		out, err := svc.Candles(c.Request.Context(), asset, market.Timeframe(tf))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/market/summary", func(c *gin.Context) {
		asset := c.Query("asset")
		tf := c.Query("timeframe")
		if asset == "" || tf == "" {
			writeError(c, http.StatusBadRequest, "invalid_request", "asset and timeframe query params are required")
			return
		}
		out, err := svc.MarketSummary(c.Request.Context(), asset, market.Timeframe(tf))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
}

func registerStrategyRoutes(g *gin.RouterGroup, svc StrategyService) {
	g.GET("/strategies", func(c *gin.Context) {
		out, err := svc.List(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/strategies/:id", func(c *gin.Context) {
		out, err := svc.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/signals", func(c *gin.Context) {
		limit := 50
		if v := c.Query("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		out, err := svc.Signals(c.Request.Context(), c.Query("asset"), c.Query("strategy"), limit)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
}

func registerBacktestRoutes(g *gin.RouterGroup, svc BacktestService) {
	// Static segments before the :id param.
	g.GET("/backtests/latest", func(c *gin.Context) {
		id, err := svc.LatestRunID(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		if id == "" {
			c.JSON(http.StatusOK, gin.H{"runId": nil})
			return
		}
		c.JSON(http.StatusOK, gin.H{"runId": id})
	})
	g.GET("/backtests/summary", func(c *gin.Context) {
		out, err := svc.DashboardSummary(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/backtests", func(c *gin.Context) {
		out, err := svc.ListRuns(c.Request.Context(), c.Query("strategy"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/backtests/:id", func(c *gin.Context) {
		out, err := svc.GetRun(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.POST("/backtests", func(c *gin.Context) {
		var req BacktestRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		run, err := svc.Execute(c.Request.Context(), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, run)
	})
}

func registerComparisonRoutes(g *gin.RouterGroup, svc ComparisonService) {
	g.POST("/comparisons", func(c *gin.Context) {
		var req ComparisonRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		out, err := svc.ExecuteComparison(c.Request.Context(), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, out)
	})
	g.GET("/comparisons", func(c *gin.Context) {
		out, err := svc.ListComparisons(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/comparisons/:id", func(c *gin.Context) {
		out, err := svc.GetComparison(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
}

func registerTradeRoutes(g *gin.RouterGroup, svc TradeService) {
	g.GET("/trades", func(c *gin.Context) {
		f := TradeFilters{
			StrategyID: c.Query("strategy"),
			Side:       c.Query("side"),
			Result:     c.Query("result"),
			From:       c.Query("from"),
			To:         c.Query("to"),
		}
		if v := c.Query("page"); v != "" {
			f.Page, _ = strconv.Atoi(v)
		}
		if v := c.Query("pageSize"); v != "" {
			f.PageSize, _ = strconv.Atoi(v)
		}
		out, err := svc.ListTrades(c.Request.Context(), f)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
}

func registerWalkForwardRoutes(g *gin.RouterGroup, svc WalkForwardService) {
	g.POST("/walkforwards", func(c *gin.Context) {
		var req WalkForwardRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		out, err := svc.ExecuteWalkForward(c.Request.Context(), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, out)
	})
	g.POST("/walkforwards/preview", func(c *gin.Context) {
		var req WalkForwardRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		out, err := svc.PreviewWalkForward(c.Request.Context(), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/walkforwards", func(c *gin.Context) {
		out, err := svc.ListWalkForwards(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/walkforwards/:id", func(c *gin.Context) {
		out, err := svc.GetWalkForward(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
}

func registerStrategyGraphRoutes(g *gin.RouterGroup, svc StrategyGraphService) {
	g.GET("/strategy-graphs", func(c *gin.Context) {
		out, err := svc.ListGraphs(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.POST("/strategy-graphs/validate", func(c *gin.Context) {
		var req ValidateGraphRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		out, err := svc.ValidateGraph(c.Request.Context(), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.POST("/strategy-graphs", func(c *gin.Context) {
		var req CreateGraphRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		out, err := svc.CreateGraph(c.Request.Context(), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, out)
	})
	g.GET("/strategy-graphs/:id", func(c *gin.Context) {
		out, err := svc.GetGraph(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/strategy-graphs/:id/versions", func(c *gin.Context) {
		out, err := svc.ListRevisions(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.POST("/strategy-graphs/:id/versions", func(c *gin.Context) {
		var req CreateRevisionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		out, err := svc.CreateRevision(c.Request.Context(), c.Param("id"), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, out)
	})
	g.POST("/strategy-graphs/:id/versions/:version/diagnostics", func(c *gin.Context) {
		rev, perr := strconv.Atoi(c.Param("version"))
		if perr != nil || rev < 1 {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid revision number")
			return
		}
		var req GraphDiagnosticsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		out, err := svc.GraphDiagnostics(c.Request.Context(), c.Param("id"), rev, req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.POST("/strategy-graphs/:id/versions/:version/backtests", func(c *gin.Context) {
		rev, perr := strconv.Atoi(c.Param("version"))
		if perr != nil || rev < 1 {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid revision number")
			return
		}
		var req GraphBacktestRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		run, err := svc.ExecuteGraphBacktest(c.Request.Context(), c.Param("id"), rev, req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusCreated, run)
	})
}

// registerSignalResearchRoutes mounts POST/GET signal-research analyses.
func registerSignalResearchRoutes(g *gin.RouterGroup, svc SignalResearchService) {
	g.POST("/signal-research", func(c *gin.Context) {
		var req SignalResearchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		out, err := svc.ExecuteSignalResearch(c.Request.Context(), req)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/signal-research", func(c *gin.Context) {
		out, err := svc.ListSignalResearch(c.Request.Context())
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	g.GET("/signal-research/:id", func(c *gin.Context) {
		out, err := svc.GetSignalResearch(c.Request.Context(), c.Param("id"))
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
}

// registerLiveMarketRoutes mounts the provider-backed live endpoints.
// They are registered before /market/candles in code order only — Gin routes
// on distinct path segments so there is no shadowing.
func registerLiveMarketRoutes(g *gin.RouterGroup, svc LiveMarketService) {
	live := g.Group("/market/live")
	live.GET("/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, svc.LiveStatus(c.Request.Context()))
	})
	live.GET("/quote", func(c *gin.Context) {
		asset := c.Query("asset")
		if asset == "" {
			writeError(c, http.StatusBadRequest, "invalid_request", "asset query param is required")
			return
		}
		out, err := svc.LiveQuote(c.Request.Context(), asset)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
	live.GET("/candles", func(c *gin.Context) {
		asset := c.Query("asset")
		tf := c.Query("timeframe")
		if asset == "" || tf == "" {
			writeError(c, http.StatusBadRequest, "invalid_request", "asset and timeframe query params are required")
			return
		}
		limit := 200
		if s := c.Query("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				limit = n
			}
		}
		out, err := svc.LiveCandles(c.Request.Context(), asset, market.Timeframe(tf), limit)
		if err != nil {
			respondErr(c, err)
			return
		}
		c.JSON(http.StatusOK, out)
	})
}
