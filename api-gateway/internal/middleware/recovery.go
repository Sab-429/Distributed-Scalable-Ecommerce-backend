package middleware

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"observability"
)

func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {

		defer func() {

			if err := recover(); err != nil {
				ctx := c.Request.Context()
				telemetry.L(ctx).ErrorContext(ctx, "panic recovered",
					"error", fmt.Sprint(err),
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
				)
				// Registering the error makes the otelgin span report Error.
				_ = c.Error(fmt.Errorf("panic: %v", err))

				c.JSON(500, gin.H{
					"error": "internal server error",
				})

				c.Abort()
			}

		}()

		c.Next()
	}
}
