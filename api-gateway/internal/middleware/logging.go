package middleware

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

func Logging() gin.HandlerFunc {
	return func(c *gin.Context) {

		start := time.Now()

		c.Next()

		duration := time.Since(start)

		requestID, exists := c.Get("request_id")

		if !exists {
			requestID = "unknown"
		}

		log.Printf(
			"method=%s path=%s status=%d duration=%s request_id=%v client=%s",
			c.Request.Method,
			c.Request.URL.Path,
			c.Writer.Status(),
			duration,
			requestID,
			c.ClientIP(),
		)
	}
}