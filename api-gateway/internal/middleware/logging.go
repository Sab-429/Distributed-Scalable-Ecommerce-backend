package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"observability"
)

// Logging emits one structured JSON log line per request. Because the shared
// logger handler pulls trace_id / span_id out of the request context, every
// line can be joined with the matching Jaeger span.
func Logging() gin.HandlerFunc {
	return func(c *gin.Context) {

		start := time.Now()

		c.Next()

		ctx := c.Request.Context()
		duration := time.Since(start)

		requestID, exists := c.Get("request_id")
		if !exists {
			requestID = "unknown"
		}

		telemetry.L(ctx).InfoContext(ctx, "http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", duration.Milliseconds(),
			"request_id", requestID,
			"client_ip", c.ClientIP(),
		)
	}
}
