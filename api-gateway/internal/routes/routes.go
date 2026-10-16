package routes

import (
	"net/http"

	"api-gateway/auth"
	"api-gateway/internal/cache"
	"api-gateway/internal/handlers"
	"api-gateway/internal/middleware"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// SetupRoutes registers all gateway routes.
//
// Route security model:
//   - /health                        → public
//   - /api/v1/auth/*                 → public (Auth service guards its own sub-routes)
//   - GET  /api/v1/products          → authenticated (any role) + Redis cached
//   - GET  /api/v1/products/:id      → authenticated (any role) + Redis cached
//   - POST /api/v1/products          → admin only
//   - PUT  /api/v1/products/:id      → admin only  + cache invalidation
//   - DELETE /api/v1/products/:id    → admin only  + cache invalidation
//   - /api/v1/orders/*               → authenticated; order-service enforces ownership
//   - /api/v1/payments/*             → authenticated; payment-service enforces ownership
func SetupRoutes(
	router *gin.Engine,
	productProxy http.Handler,
	authProxy http.Handler,
	orderProxy http.Handler,
	paymentProxy http.Handler,
	rateLimiter *middleware.RateLimiter,
	responseCache *cache.ResponseCache,
) {
	// ── Global middleware ────────────────────────────────────────────────────
	router.Use(middleware.Recovery())
	router.Use(middleware.RequestID())
	router.Use(middleware.Logging())
	router.Use(middleware.SecurityHeaders())
	router.Use(rateLimiter.Middleware())

	router.Use(cors.New(cors.Config{
		AllowOrigins: []string{
			"http://localhost:3000",
			"http://localhost:5173",
		},
		AllowMethods: []string{
			"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS",
		},
		AllowHeaders: []string{
			"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID",
		},
		AllowCredentials: true,
	}))

	// ── Shared JWT parser ────────────────────────────────────────────────────
	jwtParser := auth.NewParser()

	// ── Public routes ────────────────────────────────────────────────────────
	healthHandler := handlers.NewHealthHandler()
	router.GET("/health", healthHandler.Health)

	// Auth service is fully public at the gateway — it guards its own endpoints.
	router.Any("/api/v1/auth", gin.WrapH(authProxy))
	router.Any("/api/v1/auth/*path", gin.WrapH(authProxy))

	// ── Product routes ───────────────────────────────────────────────────────
	products := router.Group("/api/v1/products")
	products.Use(middleware.JWTAuth(jwtParser))
	{
		// Read routes — any authenticated role + Redis response cache.
		products.GET("", middleware.ResponseCache(responseCache), gin.WrapH(productProxy))
		products.GET("/*path", middleware.ResponseCache(responseCache), gin.WrapH(productProxy))

		// Mutation routes — admin only + cache invalidation on success.
		adminProducts := products.Group("")
		adminProducts.Use(middleware.RequireRoles("admin"))
		{
			adminProducts.POST("", gin.WrapH(productProxy))
			adminProducts.PUT("/*path", middleware.ResponseCacheInvalidate(responseCache), gin.WrapH(productProxy))
			adminProducts.PATCH("/*path", middleware.ResponseCacheInvalidate(responseCache), gin.WrapH(productProxy))
			adminProducts.DELETE("/*path", middleware.ResponseCacheInvalidate(responseCache), gin.WrapH(productProxy))
		}
	}

	// ── Order routes ─────────────────────────────────────────────────────────
	orders := router.Group("/api/v1/orders")
	orders.Use(middleware.JWTAuth(jwtParser))
	{
		orders.Any("", gin.WrapH(orderProxy))
		orders.Any("/*path", gin.WrapH(orderProxy))
	}

	// ── Payment routes ───────────────────────────────────────────────────────
	payments := router.Group("/api/v1/payments")
	payments.Use(middleware.JWTAuth(jwtParser))
	{
		payments.Any("", gin.WrapH(paymentProxy))
		payments.Any("/*path", gin.WrapH(paymentProxy))
	}
}
