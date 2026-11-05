package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joho/godotenv"

	"notification-worker/internal/consumer"

	"observability"
)

func main() {
	if exe, err := os.Executable(); err == nil {
		_ = godotenv.Load(filepath.Join(filepath.Dir(exe), ".env"))
	} else {
		_ = godotenv.Load()
	}

	cfg := consumer.LoadConfig()

	// ── Telemetry (traces → Jaeger, metrics → Prometheus, logs → Loki) ─────
	tel, err := telemetry.Init(context.Background(), telemetry.OptionsFromEnv("notification-worker", "1.0.0"))
	if err != nil {
		log.Fatal("telemetry init failed:", err)
	}
	telemetry.InitLogger("notification-worker")

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

	// Flush pending traces / metrics / logs before the process exits.
	telCtx, telCancel := context.WithTimeout(context.Background(), telemetry.ShutdownTimeout)
	defer telCancel()
	if err := tel.Shutdown(telCtx); err != nil {
		log.Println("telemetry shutdown:", err)
	}
	log.Println("shutting down notification-worker")
}
