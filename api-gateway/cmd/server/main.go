package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"golang.org/x/time/rate"

	"api-gateway/internal/cache"
	"api-gateway/internal/config"
	"api-gateway/internal/middleware"
	"api-gateway/internal/proxy"
	"api-gateway/internal/redis"
	"api-gateway/internal/routes"
)

func main() {
	if exe, err := os.Executable(); err == nil {
		_ = godotenv.Load(filepath.Join(filepath.Dir(exe), ".env"))
	} else {
		_ = godotenv.Load()
	}

	cfg := config.Load()

	// ── Redis ────────────────────────────────────────────────────────────────
	redisClient, err := redis.New(cfg.RedisAddr)
	if err != nil {
		log.Fatal("Redis connection failed:", err)
	}
	defer redisClient.Close()
	log.Println("Redis connected")

	responseCache := cache.NewResponseCache(redisClient)

	// ── Upstream proxies ─────────────────────────────────────────────────────
	productProxy, err := proxy.NewReverseProxy(cfg.ProductServiceURL)
	if err != nil {
		log.Fatal("Failed to create product proxy:", err)
	}

	authProxy, err := proxy.NewReverseProxy(cfg.AuthServiceURL)
	if err != nil {
		log.Fatal("Failed to create auth proxy:", err)
	}

	orderProxy, err := proxy.NewReverseProxy(cfg.OrderServiceURL)
	if err != nil {
		log.Fatal("Failed to create order proxy:", err)
	}

	paymentProxy, err := proxy.NewReverseProxy(cfg.PaymentServiceURL)
	if err != nil {
		log.Fatal("Failed to create payment proxy:", err)
	}

	rateLimiter := middleware.NewRateLimiter(
		rate.Limit(cfg.RateLimit),
		cfg.RateBurst,
	)

	// ── Router ───────────────────────────────────────────────────────────────
	router := gin.New()

	routes.SetupRoutes(
		router,
		productProxy,
		authProxy,
		orderProxy,
		paymentProxy,
		rateLimiter,
		responseCache,
	)

	// ── HTTP server with graceful shutdown ───────────────────────────────────
	server := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Println("API Gateway running on port", cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Gateway server failed:", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down API Gateway...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Gateway forced to shutdown:", err)
	}

	log.Println("API Gateway stopped")
}
