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

	"order-service/internal/clients"
	"order-service/internal/config"
	"order-service/internal/database"
	"order-service/internal/handler"
	"order-service/internal/messaging"
	"order-service/internal/middleware"
	"order-service/internal/repository"
	"order-service/internal/routes"
	"order-service/internal/service"
)

func main() {
	if exe, err := os.Executable(); err == nil {
		_ = godotenv.Load(filepath.Join(filepath.Dir(exe), ".env"))
	} else {
		_ = godotenv.Load()
	}

	cfg := config.Load()

	// ── Database ─────────────────────────────────────────────────────────────
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal("database connection failed:", err)
	}
	log.Println("database connected")

	// ── gRPC client (product-service) ─────────────────────────────────────────
	productClient, err := clients.NewProductClient(cfg.ProductServerURL)
	if err != nil {
		log.Fatal("failed to connect to product service:", err)
	}
	defer productClient.Close()

	// ── RabbitMQ (publisher connection) ──────────────────────────────────────
	rabbit, err := messaging.NewRabbitMQ(
		cfg.RabbitMQHost,
		cfg.RabbitMQPort,
		cfg.RabbitMQUser,
		cfg.RabbitMQPassword,
		cfg.RabbitMQVHost,
	)
	if err != nil {
		log.Fatal("RabbitMQ connection failed:", err)
	}
	defer rabbit.Close()

	publisher := messaging.NewPublisher(rabbit.Channel)
	if err := publisher.Setup(); err != nil {
		log.Fatal("failed to setup RabbitMQ topology: ", err)
	}
	log.Println("RabbitMQ publisher ready")

	// ── Service layer ─────────────────────────────────────────────────────────
	orderRepository := repository.NewOrderRepository(db)

	orderService := service.NewOrderService(
		orderRepository,
		productClient,
		publisher,
	)

	orderHandler := handler.NewOrderHandler(orderService)

	// ── RabbitMQ (payment consumer connection) ───────────────────────────────
	// Consumes payment.completed / payment.failed so a long-lived consumer
	// never shares a channel with the publisher.
	paymentRabbit, err := messaging.NewRabbitMQ(
		cfg.RabbitMQHost,
		cfg.RabbitMQPort,
		cfg.RabbitMQUser,
		cfg.RabbitMQPassword,
		cfg.RabbitMQVHost,
	)
	if err != nil {
		log.Fatal("RabbitMQ consumer connection failed:", err)
	}

	paymentConsumer, err := messaging.NewPaymentConsumer(paymentRabbit, orderRepository, cfg.MaxRetries)
	if err != nil {
		log.Fatal("failed to create payment consumer:", err)
	}
	if err := paymentConsumer.Setup(); err != nil {
		log.Fatal("failed to setup payment consumer topology: ", err)
	}
	if err := paymentConsumer.Start(); err != nil {
		log.Fatal("failed to start payment consumer: ", err)
	}
	log.Printf("order-service consuming from queue %q", messaging.OrderPaymentQueue)

	// ── HTTP server ───────────────────────────────────────────────────────────
	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(middleware.Logging())
	router.Use(middleware.Recovery())

	routes.Setup(router, orderHandler)

	server := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Order Service running on port %s", cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("order server failed:", err)
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("shutting down order-service...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("order server forced to shutdown:", err)
	}

	// Drain in-flight payment messages, then release the consumer connection.
	paymentConsumer.Stop()

	log.Println("order-service stopped")
}
