package database

import (
	"log"
	"os"
	"time"

	"order-service/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Connect(databaseURL string) (*gorm.DB, error) {
	// Custom logger: only warn on queries slower than 2s (NeonDB is a remote
	// serverless Postgres, so schema-inspection queries during AutoMigrate can
	// legitimately take 500ms–1s on a cold connection).
	gormLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             2 * time.Second, // raise from default 200ms
			LogLevel:                  logger.Warn,     // only log warnings and errors
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	db, err := gorm.Open(
		postgres.Open(databaseURL),
		&gorm.Config{
			Logger: gormLogger,
		},
	)
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	// Create / update tables for Order and OrderItem.
	if err := db.AutoMigrate(&models.Order{}, &models.OrderItem{}); err != nil {
		return nil, err
	}

	return db, nil
}
