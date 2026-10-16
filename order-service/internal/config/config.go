package config

import (
	"os"
	"strconv"
)

type Config struct {
	ServerPort       string
	DatabaseURL      string
	ProductServerURL string
	RabbitMQHost     string
	RabbitMQPort     string
	RabbitMQUser     string
	RabbitMQPassword string
	RabbitMQVHost    string
	MaxRetries       int
}

func Load() *Config {
	return &Config{
		ServerPort:       getEnv("SERVER_PORT", "8082"),
		DatabaseURL:      getEnv("DATABASE_URL", ""),
		ProductServerURL: getEnv("PRODUCT_SERVICE_URL", "localhost:50051"),
		RabbitMQHost:     getEnv("RABBITMQ_HOST", "localhost"),
		RabbitMQPort:     getEnv("RABBITMQ_PORT", "5672"),
		RabbitMQUser:     getEnv("RABBITMQ_USER", "admin"),
		RabbitMQPassword: getEnv("RABBITMQ_PASSWORD", "admin123"),
		RabbitMQVHost:    getEnv("RABBITMQ_VHOST", "/"),
		MaxRetries:       getIntEnv("MAX_RETRIES", 3),
	}
}

func getEnv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getIntEnv(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}
