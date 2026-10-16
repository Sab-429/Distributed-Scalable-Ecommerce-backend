package main

import (
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"product-service/internal/cache"
	"product-service/internal/config"
	"product-service/internal/database"
	"product-service/internal/handler"
	"product-service/internal/redis"
	"product-service/internal/repository"
	"product-service/internal/routes"
	"product-service/internal/service"
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

	router := gin.Default()

	// Pass the fully-initialised handler, not a bare zero-value struct.
	routes.SetupRoutes(router, productHandler)

	port := cfg.ServerPort
	if port == "" {
		port = "8080"
	}

	log.Println("Server running on port", port)

	if err := router.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
