package middleware

import (
	"errors"
	"net/http"
	"strings"

	"api-gateway/auth"

	"github.com/gin-gonic/gin"
)

// JWTAuth returns a middleware that validates the Bearer JWT on every request.
// On success it stores the parsed user ID and role in the Gin context so that
// downstream handlers and the reverse proxy can forward them to services.
//
// Usage:
//
//	protected := router.Group("/api/v1/orders")
//	protected.Use(middleware.JWTAuth(parser))
func JWTAuth(parser *auth.Parser) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authorization header required",
			})
			c.Abort()
			return
		}
		

		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "bearer token required",
			})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := parser.Parse(tokenString)
		if err != nil {
			if errors.Is(err, auth.ErrExpiredToken) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "token has expired",
				})
			} else {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "invalid token",
				})
			}
			c.Abort()
			return
		}

		userID := auth.UserIDFromClaims(claims)

		// Store parsed identity for downstream middleware and handlers.
		c.Set(string(auth.ContextUserID), userID)
		c.Set(string(auth.ContextUserRole), claims.Role)
		c.Set(string(auth.ContextClaims), claims)

		// Forward identity headers to upstream services so they don't need
		// to re-parse the token.  These headers are set on the *incoming*
		// request so the reverse proxy will carry them upstream.
		c.Request.Header.Set("X-User-ID", claims.Subject)
		c.Request.Header.Set("X-User-Role", claims.Role)

		c.Next()
	}
}
