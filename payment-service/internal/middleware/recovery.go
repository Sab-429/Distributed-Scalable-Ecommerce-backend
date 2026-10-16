package middleware

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

func Recovery() gin.HandlerFunc {

	return func(c *gin.Context) {

		defer func() {
			if err := recover(); err != nil {
				log.Printf("Panic recovered: %v", err)

				c.AbortWithStatusJSON(
					http.StatusInternalServerError,
					gin.H{
						"error": "internal server error",
					},
				)
			}
		}()
		c.Next()
	}
}
