package consumer

import (
	"os"
	"strconv"
)

type Config struct {
	Host       string
	Port       string
	User       string
	Password   string
	VHost      string
	MaxRetries int
}

func LoadConfig() Config {
	return Config{
		Host:       getEnv("RABBITMQ_HOST", "localhost"),
		Port:       getEnv("RABBITMQ_PORT", "5672"),
		User:       getEnv("RABBITMQ_USER", "admin"),
		Password:   getEnv("RABBITMQ_PASSWORD", "admin123"),
		VHost:      getEnv("RABBITMQ_VHOST", "/"),
		MaxRetries: getIntEnv("MAX_RETRIES", 3),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getIntEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
