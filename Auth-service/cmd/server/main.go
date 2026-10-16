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

	"Auth-service/internal/config"
	"Auth-service/internal/database"
	"Auth-service/internal/handler"
	"Auth-service/internal/repository"
	"Auth-service/internal/routes"
	"Auth-service/internal/service"
)

func main() {
	if exe, err := os.Executable(); err == nil {
		_ = godotenv.Load(filepath.Join(filepath.Dir(exe), ".env"))
	} else {
		_ = godotenv.Load()
	}

	cfg := config.Load()

	// ── Database ────────────────────────────────────────────────────────────
	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatal("Database connection failed:", err)
	}
	log.Println("Database connected and schema migrated")

	// ── Repositories ────────────────────────────────────────────────────────
	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewRefreshTokenRepository(db)

	// ── Services ────────────────────────────────────────────────────────────
	tokenSvc := service.NewTokenService(
		tokenRepo,
		cfg.JWTSecret,
		cfg.JWTExpiryHours,
		cfg.RefreshTokenExpiry,
		cfg.JWTIssuer,
	)

	authSvc := service.NewAuthService(userRepo, tokenSvc)

	// ── Handlers ────────────────────────────────────────────────────────────
	authHandler := handler.NewAuthHandler(authSvc)

	// ── Router ──────────────────────────────────────────────────────────────
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	routes.SetupRoutes(router, authHandler, tokenSvc)

	// ── Background: expired token cleanup (every 6 hours) ───────────────────
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := tokenRepo.DeleteExpired(); err != nil {
				log.Println("Refresh token cleanup error:", err)
			} else {
				log.Println("Expired refresh tokens purged")
			}
		}
	}()

	// ── HTTP server with graceful shutdown ───────────────────────────────────
	port := cfg.ServerPort
	if port == "" {
		port = "8081"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Println("Auth service running on port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Auth service failed:", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down auth service...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Auth service forced to shutdown:", err)
	}

	log.Println("Auth service stopped")
}
