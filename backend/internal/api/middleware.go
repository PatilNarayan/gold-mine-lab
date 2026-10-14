package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestIDHeader is read on ingress (to correlate with upstream systems) and
// written on egress so clients can quote it when reporting issues.
const RequestIDHeader = "X-Request-ID"

type ctxKey string

const requestIDKey ctxKey = "request_id"

// RequestID returns the request-scoped ID, generating one lazily if needed.
func RequestID(c *gin.Context) string {
	if v, ok := c.Get(string(requestIDKey)); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// requestID middleware: honour an inbound X-Request-ID, otherwise generate a
// random hex ID. Exposed on the response and stored in the gin context.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		c.Set(string(requestIDKey), id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is effectively impossible; fall back to time.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

// requestLogger writes one structured log line per request.
func requestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http request",
			"request_id", RequestID(c),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

// recovery converts a handler panic into a 500 using the error envelope and
// logs the panic detail server-side.
func recovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		log.Error("panic recovered",
			"request_id", RequestID(c),
			"path", c.Request.URL.Path,
			"panic", recovered,
		)
		writeError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	})
}
