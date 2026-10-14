package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthChecker is implemented by anything that can verify a dependency is
// reachable (e.g. *database.Pool). Handlers depend on this interface only.
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// healthResponse is the documented payload of GET /api/health.
type healthResponse struct {
	Status   string `json:"status"`
	Service  string `json:"service"`
	Database string `json:"database"`
}

const serviceName = "gold-mine-lab-backend"

// healthHandler reports liveness plus dependency health: 200 when the
// database is reachable, 503 ("degraded") when it is not.
func healthHandler(db HealthChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := db.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, healthResponse{
				Status:   "degraded",
				Service:  serviceName,
				Database: "down",
			})
			return
		}
		c.JSON(http.StatusOK, healthResponse{
			Status:   "ok",
			Service:  serviceName,
			Database: "up",
		})
	}
}
