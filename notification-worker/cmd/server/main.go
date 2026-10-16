package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joho/godotenv"

	"notification-worker/internal/consumer"
)

func main() {
	if exe, err := os.Executable(); err == nil {
		_ = godotenv.Load(filepath.Join(filepath.Dir(exe), ".env"))
	} else {
		_ = godotenv.Load()
	}

	cfg := consumer.LoadConfig()

	c, err := consumer.New(cfg)
	if err != nil {
		log.Fatal("failed to connect to RabbitMQ:", err)
	}
	defer c.Close()

	if err := c.Setup(); err != nil {
		log.Fatal("failed to set up RabbitMQ topology:", err)
	}

	if err := c.Start(); err != nil {
		log.Fatal("failed to start consumer:", err)
	}

	log.Printf("notification-worker consuming from queue %q (Ctrl-C to stop)", consumer.NotificationQueue)

	// Block until SIGINT/SIGTERM.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	// Graceful shutdown: stop consuming, drain in-flight messages, then
	// close the RabbitMQ connection.
	c.Stop()
	log.Println("shutting down notification-worker")
}
