package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"observability"
)

// Logging emits one structured JSON log line per request. trace_id / span_id
// are attached by the shared logger from the request context.
func Logging() gin.HandlerFunc {

	return func(c *gin.Context) {

		start := time.Now()

		c.Next()

		ctx := c.Request.Context()

		requestID, _ :=
			c.Get("request_id")

		telemetry.L(ctx).InfoContext(ctx, "http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestID,
			"client_ip", c.ClientIP(),
		)
	}
}
