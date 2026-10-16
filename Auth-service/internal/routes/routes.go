package routes

import (
	"Auth-service/internal/handler"
	"Auth-service/internal/service"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// SetupRoutes registers all auth endpoints on the given router.
//
//	Public routes  → no token required
//	Protected routes → Bearer JWT required (validated inline)
func SetupRoutes(
	router *gin.Engine,
	h *handler.AuthHandler,
	tokenSvc *service.TokenService,
) {
	v1 := router.Group("/api/v1/auth")

	// ── Public routes ─────────────────────────────────────────────────────
	v1.POST("/register", h.Register)
	v1.POST("/login", h.Login)
	v1.POST("/refresh", h.Refresh)

	// Logout only needs the refresh token in the body, no JWT check needed.
	v1.POST("/logout", h.Logout)

	// ── Protected routes ──────────────────────────────────────────────────
	protected := v1.Group("", jwtMiddleware(tokenSvc))
	{
		protected.POST("/logout-all", h.LogoutAll)
		protected.GET("/me", h.Me)
	}
}

// jwtMiddleware validates the Bearer token and stores user claims in context.
func jwtMiddleware(tokenSvc *service.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authorization header required"})
			c.Abort()
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "bearer token required"})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := tokenSvc.ValidateAccessToken(tokenString)
		if err != nil {
			if errors.Is(err, service.ErrExpiredToken) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "token has expired"})
			} else {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			}
			c.Abort()
			return
		}

		// Store user identity in the Gin context for downstream handlers.
		c.Set("user_id", claims.UserID)
		c.Set("user_role", claims.Role)
		c.Set("user_email", claims.RegisteredClaims.Subject)

		c.Next()
	}
}
