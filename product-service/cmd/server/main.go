package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"product-service/internal/cache"
	"product-service/internal/config"
	"product-service/internal/database"
	"product-service/internal/handler"
	"product-service/internal/redis"
	"product-service/internal/repository"
	"product-service/internal/routes"
	"product-service/internal/service"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	grpcserver "product-service/internal/grpc"
	pb "product-service/proto/product/v1"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Resolve .env relative to the binary's own directory so it works
	// regardless of which working directory the process is launched from.
	exe, err := os.Executable()
	if err == nil {
		envPath := filepath.Join(filepath.Dir(exe), ".env")
		if err := godotenv.Load(envPath); err != nil {
			log.Println(".env file not found, using environment variables")
		}
	} else {
		if err := godotenv.Load(); err != nil {
			log.Println(".env file not found, using environment variables")
		}
	}

	cfg := config.Load()

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatal("Database connection failed:", err)
	}
	log.Println("Database connected")

	productRepo := repository.NewProductRepository(db)

	// redis.New returns (*Client, error) — must use the constructor, not the struct directly.
	redisClient, err := redis.New(cfg.RedisAddr)
	if err != nil {
		log.Fatal("Redis connection failed:", err)
	}
	log.Println("Redis connected")

	productCache := cache.NewProductCache(redisClient)
	productService := service.NewProductService(productRepo, productCache)

	// Build the handler with its service dependency injected.
	productHandler := handler.NewProductHandler(productService)

	// ── gRPC server ──────────────────────────────────────────────────────────
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(func(
			ctx context.Context,
			req interface{},
			info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler,
		) (interface{}, error) {
			start := time.Now()
			resp, err := handler(ctx, req)
			st, _ := status.FromError(err)
			log.Printf("[gRPC] %s | %s | %s",
				info.FullMethod,
				st.Code(),
				time.Since(start),
			)
			return resp, err
		}),
	)

	productGRPCServer := grpcserver.NewProductServer(productService)

	pb.RegisterProductServiceServer(grpcServer, productGRPCServer)

	// Enable server reflection so Postman/grpcurl can discover methods automatically.
	reflection.Register(grpcServer)

	listener, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen for gRPC: %v", err)
	}

	go func() {
		log.Println("gRPC server running on port 50051")
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// ── HTTP server ───────────────────────────────────────────────────────────
	router := gin.Default()

	// Pass the fully-initialised handler, not a bare zero-value struct.
	routes.SetupRoutes(router, productHandler)

	port := cfg.ServerPort
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Product Service running on port %s (gRPC :50051)", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("product server failed:", err)
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("shutting down product-service...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop accepting new HTTP requests and finish in-flight ones.
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("product server forced to shutdown:", err)
	}

	// GracefulStop waits for in-flight gRPC RPCs to finish, then stops the
	// server. New requests are rejected during this time.
	grpcServer.GracefulStop()

	log.Println("product-service stopped")
}
