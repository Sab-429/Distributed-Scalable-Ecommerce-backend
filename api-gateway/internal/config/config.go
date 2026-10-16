package config

import "os"

type Config struct {
	ServerPort        string
	ProductServiceURL string
	AuthServiceURL    string
	OrderServiceURL   string
	PaymentServiceURL string
	RateLimit         float64
	RateBurst         int
	RedisAddr         string
}

func Load() Config {
	return Config{
		ServerPort: getEnv("SERVER_PORT", "8000"),

		ProductServiceURL: getEnv(
			"PRODUCT_SERVICE_URL",
			"http://localhost:8080",
		),

		AuthServiceURL: getEnv(
			"AUTH_SERVICE_URL",
			"http://localhost:8081",
		),

		OrderServiceURL: getEnv(
			"ORDER_SERVICE_URL",
			"http://localhost:8082",
		),

		PaymentServiceURL: getEnv(
			"PAYMENT_SERVICE_URL",
			"http://localhost:8083",
		),

		RateLimit: 10,
		RateBurst: 20,

		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
	}
}

func getEnv(key string, fallback string) string {
	value := os.Getenv(key)

	if value == "" {
		return fallback
	}
	return value
}
