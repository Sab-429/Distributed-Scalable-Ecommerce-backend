package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func UserContext() gin.HandlerFunc {

	return func(c *gin.Context) {

		userIDHeader :=
			c.GetHeader("X-User-ID")

		if userIDHeader == "" {
			c.AbortWithStatusJSON(
				http.StatusUnauthorized,
				gin.H{
					"error": "user authentication required",
				},
			)

			return
		}

		userID, err :=
			strconv.ParseUint(
				userIDHeader,
				10,
				64,
			)

		if err != nil || userID == 0 {

			c.AbortWithStatusJSON(
				http.StatusUnauthorized,
				gin.H{
					"error": "invalid user identity",
				},
			)

			return
		}

		c.Set(
			"user_id",
			uint(userID),
		)

		c.Next()
	}
}