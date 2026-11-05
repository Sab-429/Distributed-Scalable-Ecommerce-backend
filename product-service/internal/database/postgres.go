package database

import (
	"fmt"
	"product-service/internal/config"
	"product-service/internal/models"

	"observability"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func Connect(cfg config.Config)(*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost,
		cfg.DBPort,
		cfg.DBUser,
		cfg.DBPassword,
		cfg.DBName,
	)
	db, err:= gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Trace every statement and record db.client.* metrics.
	if err := db.Use(telemetry.GORMPlugin(cfg.DBName)); err != nil {
		return nil, err
	}

	err = db.AutoMigrate(&models.Product{})

	if err != nil {
		return nil, err
	}

	return db, nil
}