package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// errorEnvelope is the single JSON error shape returned by every endpoint:
//
//	{"error": {"code": "...", "message": "...", "request_id": "..."}}
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// writeError emits the standard error envelope. Internal details must never
// be passed as `message`; log them via the request logger instead.
func writeError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, errorEnvelope{
		Error: errorBody{
			Code:      code,
			Message:   message,
			RequestID: RequestID(c),
		},
	})
}

func notFoundHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		writeError(c, http.StatusNotFound, "not_found", "resource not found")
	}
}

func methodNotAllowedHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		writeError(c, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}
