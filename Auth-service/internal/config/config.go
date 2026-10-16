package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	ServerPort string

	JWTSecret           string
	JWTExpiryHours      time.Duration
	JWTIssuer           string
	RefreshTokenExpiry  time.Duration
}

func Load() Config {
	jwtExpiryHours := 24
	if h, err := strconv.Atoi(os.Getenv("JWT_EXPIRY_HOURS")); err == nil && h > 0 {
		jwtExpiryHours = h
	}

	refreshTokenExpiryDays := 7
	if d, err := strconv.Atoi(os.Getenv("REFRESH_TOKEN_EXPIRY_DAYS")); err == nil && d > 0 {
		refreshTokenExpiryDays = d
	}

	return Config{
		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     os.Getenv("DB_PORT"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),
		ServerPort: os.Getenv("SERVER_PORT"),

		JWTSecret:          os.Getenv("JWT_SECRET"),
		JWTExpiryHours:     time.Duration(jwtExpiryHours) * time.Hour,
		JWTIssuer:          os.Getenv("JWT_ISSUER"),
		RefreshTokenExpiry: time.Duration(refreshTokenExpiryDays) * 24 * time.Hour,
	}
}
